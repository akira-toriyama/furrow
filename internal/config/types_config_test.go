package config

import (
	"strings"
	"testing"
)

// TestTypesDefaults pins t-3jd1 §2: absent [types] falls back to the built-in
// vocabulary, so every board — including the central board and the fleet, which
// carry no [types] table yet — still treats "epic" as a container (hole ③). A
// type-less task resolves to the non-container default and stays actionable.
func TestTypesDefaults(t *testing.T) {
	for name, c := range map[string]*Config{
		"Default()":       Default(),
		"absent [types]":  mustLoad(t, ""),
		"unrelated table": mustLoad(t, "[lanes]\norder = [\"inbox\", \"done\"]\n"),
	} {
		if !equal(c.Types, []string{"task", "epic"}) {
			t.Errorf("%s: Types = %v, want [task epic]", name, c.Types)
		}
		if c.DefaultType != "task" {
			t.Errorf("%s: DefaultType = %q, want task", name, c.DefaultType)
		}
		if !c.IsContainerType("epic") {
			t.Errorf("%s: epic must be a container", name)
		}
		if c.IsContainerType("") {
			t.Errorf("%s: a type-less task (empty) must NOT be a container", name)
		}
		if c.IsContainerType("task") {
			t.Errorf("%s: task must not be a container", name)
		}
		if c.EffectiveType("") != "task" {
			t.Errorf("%s: empty type must resolve to the default", name)
		}
		if !c.IsType("") || !c.IsType("epic") || c.IsType("epci") {
			t.Errorf("%s: IsType vocabulary check wrong", name)
		}
	}
}

// TestTypesDefaultContainerClamps pins hole ②: a [types].default that is a
// container would resolve every type-less shard to a box and hide it from
// `furrow next`. The loader must clamp it back to a non-container and warn.
func TestTypesDefaultContainerClamps(t *testing.T) {
	cfg, warn := mustLoadWarn(t, "[types]\norder = [\"task\", \"epic\"]\ndefault = \"epic\"\ncontainers = [\"epic\"]\n")
	if cfg.DefaultType != "task" {
		t.Errorf("a container default must clamp to a non-container; got %q", cfg.DefaultType)
	}
	if cfg.IsContainerType("") {
		t.Error("after clamping, a type-less task must not be a container")
	}
	if !warns(warn, "container type") {
		t.Errorf("clamping a container default must warn; got %v", warn)
	}
}

// TestTypesUnknownDefaultClamps: a default not in the vocabulary clamps + warns.
func TestTypesUnknownDefaultClamps(t *testing.T) {
	cfg, warn := mustLoadWarn(t, "[types]\norder = [\"task\", \"epic\"]\ndefault = \"frob\"\n")
	if cfg.DefaultType != "task" {
		t.Errorf("an unknown default must clamp; got %q", cfg.DefaultType)
	}
	if !warns(warn, "not in types.order") {
		t.Errorf("an unknown default must warn; got %v", warn)
	}
}

// TestTypesUnknownContainerIgnored: a container entry that is not a type is
// dropped with a warning (clamp-don't-reject), like an unknown terminal lane.
func TestTypesUnknownContainerIgnored(t *testing.T) {
	cfg, warn := mustLoadWarn(t, "[types]\norder = [\"task\", \"epic\"]\ncontainers = [\"epic\", \"ghost\"]\n")
	if !cfg.IsContainerType("epic") || cfg.Containers["ghost"] {
		t.Errorf("only real types may be containers; got %v", cfg.Containers)
	}
	if !warns(warn, "is not a type") {
		t.Errorf("an unknown container must warn; got %v", warn)
	}
}

// TestTypesCustomVocab: a custom vocabulary with several containers works, and
// ContainerTypes reports them in vocabulary order.
func TestTypesCustomVocab(t *testing.T) {
	cfg := mustLoad(t, "[types]\norder = [\"task\", \"spike\", \"epic\", \"milestone\"]\ncontainers = [\"epic\", \"milestone\"]\n")
	if !cfg.IsContainerType("epic") || !cfg.IsContainerType("milestone") || cfg.IsContainerType("spike") {
		t.Errorf("custom containers wrong: %v", cfg.Containers)
	}
	if !equal(cfg.ContainerTypes(), []string{"epic", "milestone"}) {
		t.Errorf("ContainerTypes = %v, want [epic milestone] in vocab order", cfg.ContainerTypes())
	}
}

func mustLoad(t *testing.T, toml string) *Config {
	t.Helper()
	c, _, err := Load(writeTOML(t, toml))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustLoadWarn(t *testing.T, toml string) (*Config, []string) {
	t.Helper()
	c, warn, err := Load(writeTOML(t, toml))
	if err != nil {
		t.Fatal(err)
	}
	return c, warn
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func warns(warn []string, substr string) bool {
	for _, w := range warn {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
