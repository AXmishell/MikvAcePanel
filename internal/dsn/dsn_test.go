package dsn

import (
	"strings"
	"testing"
)

func TestTemplatesAreUniqueAndValid(t *testing.T) {
	all := All()
	if len(all.Postgres) == 0 || len(all.Redis) == 0 {
		t.Fatalf("templates = %+v", all)
	}
	for _, group := range [][]Template{all.Postgres, all.Redis} {
		seen := map[string]bool{}
		for _, tpl := range group {
			if tpl.ID == "" || tpl.Label == "" || tpl.Value == "" {
				t.Fatalf("incomplete template: %+v", tpl)
			}
			if seen[tpl.ID] {
				t.Fatalf("duplicate template id %q", tpl.ID)
			}
			seen[tpl.ID] = true
			if !strings.Contains(tpl.Value, "HOST") {
				t.Fatalf("template %q has no HOST placeholder", tpl.ID)
			}
		}
	}
}

func TestValidatePostgres(t *testing.T) {
	valid := []string{
		"postgres://user:pass@db:5432/mikvace?sslmode=disable",
		"postgresql://user:pass@db:5432/mikvace",
		"host=db port=5432 user=user password=pass dbname=mikvace sslmode=disable",
		"postgres:///mikvace?host=/var/run/postgresql",
	}
	for _, value := range valid {
		if err := ValidatePostgres(value); err != nil {
			t.Errorf("ValidatePostgres(%q) = %v", value, err)
		}
	}
	invalid := []string{
		"",
		"mysql://user:pass@db:3306/mikvace",
		"postgres://user:pass@/mikvace",
		"postgres://user:pass@db:5432/",
		"user=user password=pass",
	}
	for _, value := range invalid {
		if err := ValidatePostgres(value); err == nil {
			t.Errorf("ValidatePostgres(%q) accepted invalid DSN", value)
		}
	}
}

func TestTemplateValuesAreValid(t *testing.T) {
	for _, tpl := range Templates(Postgres) {
		if tpl.ID == "postgres-keyword" {
			if err := ValidatePostgres(tpl.Value); err != nil {
				t.Errorf("%s: %v", tpl.ID, err)
			}
			continue
		}
		if err := ValidatePostgres(tpl.Value); err != nil {
			t.Errorf("%s: %v", tpl.ID, err)
		}
	}
}
