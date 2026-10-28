package config

import "testing"

func TestDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" || cfg.DBPath != "data/knowledge.db" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestRejectUnsafeOrInvalidSettings(t *testing.T) {
	for _, args := range [][]string{
		{"-addr", "0.0.0.0:8080"}, {"-addr", ":8080"},
		{"-addr", "[::]:8080"}, {"-addr", "example.com:8080"},
		{"-addr", "127.0.0.1:abc"}, {"-addr", "127.0.0.1:65536"},
		{"-db", ""}, {"extra"}, {"-unknown"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			if _, err := Parse(args); err == nil {
				t.Fatalf("accepted invalid args %q", args)
			}
		})
	}
}

func TestExplicitSettings(t *testing.T) {
	cfg, err := Parse([]string{"-addr", "[::1]:9000", "-db", "custom.db"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "[::1]:9000" || cfg.DBPath != "custom.db" {
		t.Fatalf("unexpected settings: %+v", cfg)
	}
}
