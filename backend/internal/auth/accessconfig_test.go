package auth

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/domain"
)

func TestRepositoryAccessConfigLoads(t *testing.T) {
	cfg, err := LoadAccessConfig(filepath.Join("..", "..", "..", "infra", "access-config.toml"))
	if err != nil {
		t.Fatalf("infra/access-config.toml: %v", err)
	}
	if cfg.HomeOrganization() != "demo-org" {
		t.Fatalf("home organization = %q", cfg.HomeOrganization())
	}
	want := map[string]string{
		"learn-admin": domain.RoleOrganizationAdmin, "learn-trainer": domain.RoleTrainer,
		"learn-manager": domain.RoleTrainingManager, "learn-coordinator": domain.RoleCoordinator,
	}
	for _, b := range cfg.Bindings() {
		if want[b.Group] != b.Role {
			t.Errorf("binding %s -> %s unexpected", b.Group, b.Role)
		}
		delete(want, b.Group)
	}
	if len(want) != 0 {
		t.Fatalf("missing bindings: %v", want)
	}
	if _, ok := cfg.PermissionsForRole(domain.RoleTrainer)[domain.PermStaffManage]; ok {
		t.Fatal("trainers must not hold staff.manage")
	}
	if _, ok := cfg.PermissionsForRole(domain.RoleCoordinator)[domain.PermQualificationManage]; !ok {
		t.Fatal("coordinators manage qualifications")
	}
}

func TestParseAccessConfig(t *testing.T) {
	cfg, err := parseAccessConfig([]byte(`
organization = "demo-org"

[roles.trainer]
description = "Teaches"
permissions = ["course.teach"]

[[bindings]]
group = "  Learn-Trainer "
role = "trainer"

[[bindings]]
group = "learn-admin"
role = "organization_admin"
organization = "other-org"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PermissionsForRole(domain.RoleTrainer); len(got) != 1 {
		t.Fatalf("a listed role replaces the built-in set: %v", got)
	}
	if cfg.PermissionsForRole(domain.RoleTrainingManager) == nil {
		t.Fatal("built-in roles not listed in the file stay")
	}
	if cfg.RoleDescription(domain.RoleTrainer) != "Teaches" || !cfg.HasRole(domain.RoleOrganizationAdmin) {
		t.Fatal("descriptions or built-in roles lost")
	}
	if !slices.Contains(cfg.Roles(), domain.RoleCoordinator) {
		t.Fatalf("roles = %v", cfg.Roles())
	}
	got := cfg.BindingsFor([]string{"/LEARN-TRAINER", "/learn-admin"})
	if len(got) != 2 || got[0].Organization != "demo-org" || got[1].Organization != "other-org" {
		t.Fatalf("bindings = %+v", got)
	}
	if len(cfg.BindingsFor(nil)) != 0 || len(cfg.BindingsFor([]string{"learn"})) != 0 {
		t.Fatal("unrelated groups matched")
	}
}

func TestParseAccessConfigErrors(t *testing.T) {
	for name, tc := range map[string]struct{ toml, want string }{
		"unknown permission": {`[roles.x]
permissions = ["course.fly"]`, `unknown permission "course.fly"`},
		"unknown role": {`organization = "o"
[[bindings]]
group = "g"
role = "nobody"`, `unknown role "nobody"`},
		"no organization": {`[[bindings]]
group = "g"
role = "trainer"`, "organization is required"},
		"no group": {`organization = "o"
[[bindings]]
role = "trainer"`, "group is required"},
		"bad toml": {`roles = [`, "parse toml"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseAccessConfig([]byte(tc.toml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := LoadAccessConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("missing file accepted")
	}
	path := filepath.Join(t.TempDir(), "a.toml")
	if err := os.WriteFile(path, []byte(`[roles.x]
permissions = ["nope"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAccessConfig(path); err == nil || !strings.Contains(err.Error(), "a.toml") {
		t.Fatalf("error should name the file: %v", err)
	}
}

func TestDefaultAccessConfig(t *testing.T) {
	cfg := DefaultAccessConfig()
	if len(cfg.Bindings()) != 0 || cfg.HomeOrganization() != "" {
		t.Fatal("the default catalog binds nothing")
	}
	if len(cfg.PermissionsForRole(domain.RoleOrganizationAdmin)) != len(domain.AllPermissions()) {
		t.Fatal("organization_admin holds every permission")
	}
	var nilCfg *AccessConfig
	if nilCfg.PermissionsForRole("x") != nil || nilCfg.HasRole("x") || nilCfg.Roles() != nil || nilCfg.BindingsFor([]string{"g"}) != nil {
		t.Fatal("nil config should grant nothing")
	}
}
