package ajean

import (
	"reflect"
	"testing"
)

func TestTranslateLoadMode(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		supported bool
		want      []string
	}{
		{
			name:      "old engine: leave flags untouched",
			args:      []string{"-m", "x.gguf", "--mlock", "--no-mmap"},
			supported: false,
			want:      []string{"-m", "x.gguf", "--mlock", "--no-mmap"},
		},
		{
			name:      "no flags: unchanged",
			args:      []string{"-m", "x.gguf", "-c", "4096"},
			supported: true,
			want:      []string{"-m", "x.gguf", "-c", "4096"},
		},
		{
			name:      "mlock only -> mmap+mlock",
			args:      []string{"-m", "x.gguf", "--mlock"},
			supported: true,
			want:      []string{"-m", "x.gguf", "--load-mode", "mmap+mlock"},
		},
		{
			name:      "no-mmap only -> none",
			args:      []string{"-m", "x.gguf", "--no-mmap"},
			supported: true,
			want:      []string{"-m", "x.gguf", "--load-mode", "none"},
		},
		{
			name:      "both -> mlock (no mmap + resident)",
			args:      []string{"--mlock", "-m", "x.gguf", "--no-mmap"},
			supported: true,
			want:      []string{"-m", "x.gguf", "--load-mode", "mlock"},
		},
		{
			name:      "old engine: --load-mode none -> --no-mmap",
			args:      []string{"-m", "x.gguf", "--load-mode", "none"},
			supported: false,
			want:      []string{"-m", "x.gguf", "--no-mmap"},
		},
		{
			name:      "old engine: --load-mode mlock -> --mlock --no-mmap",
			args:      []string{"-m", "x.gguf", "--load-mode", "mlock", "-c", "4096"},
			supported: false,
			want:      []string{"-m", "x.gguf", "-c", "4096", "--mlock", "--no-mmap"},
		},
		{
			name:      "old engine: -lm mmap+mlock -> --mlock",
			args:      []string{"-lm", "mmap+mlock", "-m", "x.gguf"},
			supported: false,
			want:      []string{"-m", "x.gguf", "--mlock"},
		},
		{
			name:      "old engine: --load-mode auto -> nothing",
			args:      []string{"-m", "x.gguf", "--load-mode", "auto"},
			supported: false,
			want:      []string{"-m", "x.gguf"},
		},
		{
			name:      "explicit --load-mode wins, old flag stripped",
			args:      []string{"-m", "x.gguf", "--mlock", "--load-mode", "dio"},
			supported: true,
			want:      []string{"-m", "x.gguf", "--load-mode", "dio"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := translateLoadMode(c.args, c.supported)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("translateLoadMode(%v, %v) = %v, want %v", c.args, c.supported, got, c.want)
			}
		})
	}
}
