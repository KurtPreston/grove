package main

import (
	"reflect"
	"strings"
	"testing"

	"grove/internal/config"
)

func TestPopSparse(t *testing.T) {
	// Both flag forms are removed and repeated values merge; other args keep their order
	args, f := popSparse([]string{"feature", "--sparse", "apps/web,ci", "--from", "main", "--sparse=bazel"})
	if strings.Join(args, " ") != "feature --from main" {
		t.Errorf("popSparse args = %v, want [feature --from main]", args)
	}
	if want := []string{"apps/web", "ci", "bazel"}; !reflect.DeepEqual(f.Paths, want) || f.Full {
		t.Errorf("popSparse flags = %+v, want Paths %v", f, want)
	}

	// --full on its own, and no flags at all
	if _, f := popSparse([]string{"feature", "--full"}); !f.Full || f.Paths != nil {
		t.Errorf("popSparse(--full) = %+v, want Full only", f)
	}
	if _, f := popSparse([]string{"feature"}); f.Full || f.Paths != nil {
		t.Errorf("popSparse() = %+v, want empty", f)
	}
}

func TestSparseForCreate(t *testing.T) {
	cfg := config.Config{Sparse: []string{"apps/web/"}}
	cases := map[string]struct {
		flags sparseFlags
		cfg   config.Config
		want  []string
	}{
		"config default":    {sparseFlags{}, cfg, []string{"apps/web"}},
		"no config":         {sparseFlags{}, config.Config{}, nil},
		"--sparse wins":     {sparseFlags{Paths: []string{"ci"}}, cfg, []string{"ci"}},
		"--full wins":       {sparseFlags{Full: true}, cfg, nil},
		"--sparse w/o conf": {sparseFlags{Paths: []string{"ci"}}, config.Config{}, []string{"ci"}},
	}
	for name, c := range cases {
		if got := c.flags.forCreate(c.cfg); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: forCreate = %v, want %v", name, got, c.want)
		}
	}
}
