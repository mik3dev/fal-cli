// Package upload sends local files to fal storage and rewrites *_url inputs.
package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/output"
)

const (
	maxSingle   = 90 << 20 // fal single-shot upload limit; multipart above it
	storageType = "fal-cdn-v3"
	defaultMime = "application/octet-stream"
)

// IsFileKey reports whether an input key names a file reference.
// ponytail: name heuristic only; stage 2 adds ui.field and description from the schema.
func IsFileKey(key string) bool {
	return strings.HasSuffix(key, "_url") || strings.HasSuffix(key, "_urls")
}

// One uploads path and returns its public URL: initiate on the storage host,
// then PUT the bytes to the presigned upload_url (no auth header).
func One(ctx context.Context, c *api.Client, path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", &output.Error{Code: output.CodeIO, Message: fmt.Sprintf("cannot read %s: %v", path, err)}
	}
	if st.Size() > maxSingle {
		return "", &output.Error{Code: output.CodeIO, Message: fmt.Sprintf("%s is larger than 90 MiB; multipart upload is not supported yet", path)}
	}
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if ct == "" {
		ct = defaultMime
	}
	var init struct {
		FileURL   string `json:"file_url"`
		UploadURL string `json:"upload_url"`
	}
	req := api.Request{
		Method: http.MethodPost, Path: "/storage/upload/initiate", Host: api.HostStorage,
		Query: url.Values{"storage_type": {storageType}},
		Body:  map[string]string{"content_type": ct, "file_name": filepath.Base(path)},
	}
	if _, err := c.Do(ctx, req, &init); err != nil {
		return "", err
	}
	if init.FileURL == "" || init.UploadURL == "" {
		return "", &api.Error{Status: 200, Message: "upload initiate response lacks file_url/upload_url"}
	}
	if err := put(ctx, c, init.UploadURL, ct, path, st.Size()); err != nil {
		return "", err
	}
	return init.FileURL, nil
}

func put(ctx context.Context, c *api.Client, uploadURL, ct, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return &output.Error{Code: output.CodeIO, Message: fmt.Sprintf("cannot read %s: %v", path, err)}
	}
	defer func() { _ = f.Close() }()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, f)
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	hreq.ContentLength = size
	hreq.Header.Set("Content-Type", ct)
	resp, err := c.UploadHTTP.Do(hreq)
	if err != nil {
		return &api.NetError{Sent: true, Err: errors.New("upload PUT failed: " + scrubURL(err.Error(), uploadURL))}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &api.Error{Status: resp.StatusCode, Message: fmt.Sprintf("upload of %s rejected: %s", filepath.Base(path), strings.TrimSpace(string(body)))}
	}
	return nil
}

// scrubURL hides the presigned URL (it carries a signature) from error text.
func scrubURL(msg, u string) string {
	return strings.ReplaceAll(msg, u, "<upload_url>")
}

// Resolve returns a copy of input where every local file named by a *_url /
// *_urls key (string or []any of strings) is uploaded and replaced by its
// URL. URLs (http, https, data) and missing paths are left untouched.
func Resolve(ctx context.Context, c *api.Client, input map[string]any, exists func(string) bool) (map[string]any, []string, error) {
	out := make(map[string]any, len(input))
	var uploaded []string
	up := func(v any) (any, error) {
		s, ok := v.(string)
		if !ok || isRemote(s) || !exists(s) {
			return v, nil
		}
		u, err := One(ctx, c, s)
		if err != nil {
			return nil, err
		}
		uploaded = append(uploaded, s)
		return u, nil
	}
	for k, v := range input {
		if !IsFileKey(k) {
			out[k] = v
			continue
		}
		if items, ok := v.([]any); ok {
			cp := make([]any, len(items))
			for i, it := range items {
				r, err := up(it)
				if err != nil {
					return nil, uploaded, err
				}
				cp[i] = r
			}
			out[k] = cp
			continue
		}
		r, err := up(v)
		if err != nil {
			return nil, uploaded, err
		}
		out[k] = r
	}
	return out, uploaded, nil
}

func isRemote(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "data:")
}
