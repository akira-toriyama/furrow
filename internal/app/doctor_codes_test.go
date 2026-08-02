package app

import (
	"os"
	"regexp"
	"testing"
)

// The doctor finding-code vocabulary has one emission file (doctor.go) and one
// registry (doctorCodes). This pins them to each other in BOTH directions —
// the same discipline as TestLintCodeRegistryCoversEmitted, plus the reverse
// pass a single-file vocabulary makes possible: an emitted code missing from
// the registry weakens every docs claim built on `furrow vocab doctor-codes`,
// and a registered code no longer emitted is rename-rot the forward pass
// cannot see.
func TestDoctorCodeRegistryCoversEmitted(t *testing.T) {
	src, err := os.ReadFile("doctor.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`Code: "([a-z][a-z-]*)"`)
	emitted := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		emitted[m[1]] = true
	}
	if len(emitted) < 10 {
		t.Fatalf("grep found only %d codes — the pattern likely broke, not the registry", len(emitted))
	}
	for c := range emitted {
		if !doctorCodes[c] {
			t.Errorf("doctor code %q emitted in doctor.go is NOT in doctorCodes — register it (else `furrow vocab doctor-codes` under-reports the vocabulary)", c)
		}
	}
	for c := range doctorCodes {
		if !emitted[c] {
			t.Errorf("doctor code %q is registered but no longer emitted in doctor.go — a rename left the registry (and any docs naming it) behind", c)
		}
	}
}
