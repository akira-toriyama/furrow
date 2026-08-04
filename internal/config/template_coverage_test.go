package config

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestTemplateCoversEveryShippedKey pins the discoverability rule: every key
// the board-config parser reads MUST appear in the template `furrow init`
// scaffolds (live or commented) — an agent reads --help and the file it was
// handed, not the README, so a key absent from the template may as well not
// exist. Derived by reflection from the raw decode struct (the same source as
// TopLevelKeys), so a key added to the parser fails here until the template
// names it.
func TestTemplateCoversEveryShippedKey(t *testing.T) {
	rt := reflect.TypeOf(raw{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := tomlTag(f)
		if tag == "" {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Struct: // a [section] with leaf keys
			section := "[" + tag + "]"
			if !strings.Contains(Template, section) {
				t.Errorf("template is missing section %s", section)
				continue
			}
			for j := 0; j < f.Type.NumField(); j++ {
				leaf := tomlTag(f.Type.Field(j))
				if leaf == "" {
					continue
				}
				if !keyAppears(Template, leaf) {
					t.Errorf("template is missing key %q of section %s (live or commented)", leaf, section)
				}
			}
		case reflect.Map: // [alias]: free-form keys, the section itself must show
			if !strings.Contains(Template, "["+tag+"]") {
				t.Errorf("template is missing section [%s]", tag)
			}
		default: // a bare top-level key (standalone, default_repo)
			if !keyAppears(Template, tag) {
				t.Errorf("template is missing top-level key %q (live or commented)", tag)
			}
		}
	}
}

// keyAppears reports whether the template shows `key = ...` live or commented
// (`# key = ...`), at line start — a mention in prose does not count, because
// the point is a copy-uncomment-edit affordance.
func keyAppears(template, key string) bool {
	re := regexp.MustCompile(`(?m)^(# )?` + regexp.QuoteMeta(key) + ` = `)
	return re.MatchString(template)
}

func tomlTag(f reflect.StructField) string {
	tag := f.Tag.Get("toml")
	if idx := strings.IndexByte(tag, ','); idx >= 0 {
		tag = tag[:idx]
	}
	if tag == "-" {
		return ""
	}
	return tag
}
