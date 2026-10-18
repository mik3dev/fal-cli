package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsInstallToTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills")
	code, m, _ := run(t, map[string]string{}, "skills", "install", "--target", target)
	files, _ := m["files"].([]any)
	if code != 0 || m["target"] != target || len(files) != 4 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	for _, want := range []string{"fal-generate/SKILL.md", "references/image.md", "references/video.md", "references/audio.md"} {
		found := false
		for _, f := range files {
			if strings.HasSuffix(f.(string), filepath.FromSlash(want)) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s not installed: %v", want, files)
		}
	}
}

func TestSkillsInstallNoHomeIsUsage(t *testing.T) {
	code, m, _ := run(t, map[string]string{"HOME": "", "USERPROFILE": ""}, "skills", "install")
	e, _ := m["error"].(map[string]any)
	if code != 2 || e["code"] != "usage" || !strings.Contains(e["message"].(string), "--target") {
		t.Fatalf("code=%d m=%v", code, m)
	}
}
