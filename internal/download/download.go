// Package download saves result media locally without ever overwriting.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/result"
)

// File is one downloaded result.
type File struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

// All downloads every media into outDir as <endpoint>_<requestID>_<n><ext>.
// GETs are bare (no Authorization): result files live on the fal CDN.
func All(ctx context.Context, hc *http.Client, media []result.Media, outDir, endpoint, requestID string) ([]File, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, ioError(err.Error(), "")
	}
	client := downloadClient(hc)
	files := make([]File, 0, len(media))
	for i, m := range media {
		f, err := one(ctx, client, m, outDir, fmt.Sprintf("%s_%s_%d", safeToken(endpoint), safeToken(requestID), i))
		if err != nil {
			return files, err
		}
		files = append(files, f)
	}
	return files, nil
}

// safeToken keeps [A-Za-z0-9._-], maps everything else to "-" and removes
// ".." so the token can never act as a path component.
func safeToken(s string) string {
	b := []rune(s)
	for i, r := range b {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			b[i] = '-'
		}
	}
	out := string(b)
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "-")
	}
	if out == "" || out == "." {
		return "x"
	}
	return out
}

func one(ctx context.Context, hc *http.Client, m result.Media, outDir, base string) (File, error) {
	if err := requireHTTPS(m.URL); err != nil {
		return File{}, ioError(err.Error(), m.URL)
	}
	resp, err := get(ctx, hc, m.URL)
	if err != nil {
		return File{}, ioError(err.Error(), m.URL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return File{}, ioError(fmt.Sprintf("HTTP %d", resp.StatusCode), m.URL)
	}
	ext, kind := extAndKind(m, resp.Header.Get("Content-Type"))
	dest, err := save(ctx, resp.Body, outDir, base, ext)
	if err != nil {
		return File{}, ioError(err.Error(), m.URL)
	}
	return File{Path: dest, URL: m.URL, Kind: kind}, nil
}

func requireHTTPS(u string) error {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host == "" {
		return errors.New("only https result URLs are downloaded")
	}
	return nil
}

// get issues a bare GET: no Authorization, no API key in any form.
func get(ctx context.Context, hc *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL: signed URLs carry tokens
		}
		return nil, err
	}
	return resp, nil
}

// headerTimeout bounds the wait for response headers.
var headerTimeout = 60 * time.Second

// downloadClient derives the CDN client from hc: no overall Timeout (long
// bodies), a response-header timeout, https-only redirects. hc's transport
// is cloned, never mutated.
func downloadClient(hc *http.Client) *http.Client {
	cp := http.Client{}
	rt := http.DefaultTransport
	if hc != nil {
		cp = *hc
		if hc.Transport != nil {
			rt = hc.Transport
		}
	}
	if t, ok := rt.(*http.Transport); ok {
		clone := t.Clone()
		clone.ResponseHeaderTimeout = headerTimeout
		rt = clone
	}
	cp.Transport = rt
	cp.Timeout = 0
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("refusing redirect to non-https URL")
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cp
}

// extAndKind picks the extension from the media content type, else the
// response header, else the URL path; kind from whichever MIME type is known.
func extAndKind(m result.Media, header string) (ext, kind string) {
	mt := mediaType(m.ContentType)
	if mt == "" {
		mt = mediaType(header)
	}
	if mt != "" {
		ext = extForType(mt)
	}
	if ext == "" {
		if p, err := url.Parse(m.URL); err == nil {
			ext = path.Ext(p.Path)
		}
	}
	if ext == "" {
		ext = ".bin"
	}
	if mt == "" {
		mt = mime.TypeByExtension(ext)
	}
	return ext, kindOf(mt)
}

// genericTypes carry no information about the file; the URL decides instead.
var genericTypes = map[string]bool{"text/plain": true, "application/octet-stream": true, "binary/octet-stream": true}

func mediaType(ct string) string {
	mt, _, _ := mime.ParseMediaType(ct)
	if genericTypes[mt] {
		return ""
	}
	return mt
}

// preferredExt pins the extension for common types (fal names files .jpeg).
var preferredExt = map[string]string{
	"video/mp4": ".mp4", "audio/mpeg": ".mp3", "image/jpeg": ".jpeg",
	"image/png": ".png", "image/webp": ".webp", "audio/wav": ".wav", "audio/x-wav": ".wav",
}

func extForType(mt string) string {
	if e, ok := preferredExt[mt]; ok {
		return e
	}
	exts, _ := mime.ExtensionsByType(mt)
	if len(exts) == 0 {
		return ""
	}
	sort.Strings(exts)
	return exts[0]
}

func kindOf(mt string) string {
	for _, k := range []string{"image", "video", "audio"} {
		if strings.HasPrefix(mt, k+"/") {
			return k
		}
	}
	return "file"
}

// save streams body to a temp .part file, then hard-links it to the first free
// name (base+ext, base-1+ext, ...). Link fails if the target exists, so an
// existing file is never replaced, even under a race.
func save(ctx context.Context, body io.Reader, outDir, base, ext string) (string, error) {
	part, err := os.CreateTemp(outDir, ".fal-*.part")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(part.Name()) }()
	if _, err := io.Copy(part, body); err != nil {
		_ = part.Close()
		return "", err
	}
	if err := ctx.Err(); err != nil {
		_ = part.Close()
		return "", err
	}
	if err := part.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(part.Name(), 0o644); err != nil {
		return "", err
	}
	for n := 0; ; n++ {
		name := base + ext
		if n > 0 {
			name = fmt.Sprintf("%s-%d%s", base, n, ext)
		}
		dest := filepath.Join(outDir, name)
		if filepath.Dir(dest) != filepath.Clean(outDir) {
			return "", errors.New("refusing to write outside the output directory")
		}
		err := linkFile(part.Name(), dest)
		if err != nil && !errors.Is(err, os.ErrExist) {
			err = copyExclusive(part.Name(), dest) // no hard links here (FAT, network mounts)
		}
		if err == nil {
			return dest, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}

// linkFile is a variable so tests can simulate filesystems without hard links.
var linkFile = os.Link

// copyExclusive creates dest with O_EXCL (never replaces) and copies src into it.
func copyExclusive(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func ioError(msg, u string) *output.Error {
	e := &output.Error{Code: output.CodeIO, Message: "download failed: " + msg}
	if u != "" {
		e.Details = map[string]any{"url": u}
	}
	return e
}
