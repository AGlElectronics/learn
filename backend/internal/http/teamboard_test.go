package http_test

// Team progress boards: percentages per group, never names, access and
// enabled-only. Same DB-backed setup as courses_test.go.

import (
	"encoding/json"
	"strings"
	"testing"
)

func teamGroups(t *testing.T, board map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, g := range arr(board, "groups") {
		out[str(obj(g), "group_name")] = obj(g)
	}
	return out
}

func wantTeam(t *testing.T, g map[string]any, members, complete, percent int, what string) {
	t.Helper()
	if g == nil || num(g, "members_count") != float64(members) || num(g, "complete_count") != float64(complete) ||
		num(g, "percent") != float64(percent) {
		t.Fatalf("%s: got %v, want members=%d complete=%d percent=%d", what, g, members, complete, percent)
	}
}

// wantHidden: groups under three members show no numbers at all.
func wantHidden(t *testing.T, g map[string]any, what string) {
	t.Helper()
	if g == nil || g["members_count"] != nil || g["complete_count"] != nil || g["percent"] != nil ||
		str(g, "hidden_reason") != "too_small" {
		t.Fatalf("%s: got %v, want a hidden too_small group", what, g)
	}
}

func TestTeamBoard(t *testing.T) {
	h, pool := setupQualRouter(t, nil, nil)
	red1 := identity{user: "red-one", groups: "team-red"}
	red2 := identity{user: "red-two", groups: "team-red"}
	red3 := identity{user: "red-three", groups: "team-red"}
	blue := identity{user: "blue-one", groups: "team-blue"}
	outsider := identity{user: "red-outsider", groups: "team-red"}
	ids := signIn(t, h, admin, trainerA, trainerB, red1, red2, red3, blue, outsider)

	courseID, sectionID := newCourse(t, h, "Team course")
	pageID := newActivity(t, h, sectionID, "page", "Read me")
	publishAndEnrol(t, h, courseID, ids[red1.user], ids[red2.user], ids[red3.user], ids[blue.user])
	for _, who := range []identity{red1, red2} {
		code, _ := call(t, h, who, "PUT", "/api/v1/activities/"+pageID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete page "+who.user)
	}
	board := "/api/v1/courses/" + courseID + "/team-board"
	settings := board + "/settings"

	t.Run("disabled board is staff-only", func(t *testing.T) {
		code, _ := call(t, h, red1, "GET", board, nil)
		mustStatus(t, code, 404, "learner reads disabled board")
		code, b := call(t, h, trainerA, "GET", board, nil)
		mustStatus(t, code, 200, "trainer previews disabled board")
		if b["enabled"] != false {
			t.Fatalf("preview: %v", b)
		}
	})

	t.Run("settings need edit rights", func(t *testing.T) {
		code, _ := call(t, h, red1, "PUT", settings, map[string]any{"enabled": true})
		mustStatus(t, code, 403, "learner changes settings")
		code, _ = call(t, h, trainerB, "PUT", settings, map[string]any{"enabled": true})
		mustStatus(t, code, 403, "other trainer changes settings")
		code, s := call(t, h, trainerA, "PUT", settings,
			map[string]any{"enabled": true, "groups": []string{"team-red", " team-blue ", "team-red", "team-empty"}})
		mustStatus(t, code, 200, "enable board")
		groups := arr(s, "groups")
		if s["enabled"] != true || len(groups) != 3 || groups[0] != "team-blue" || groups[2] != "team-red" {
			t.Fatalf("settings: %v", s)
		}
		code, s = call(t, h, trainerA, "PUT", settings, map[string]any{"groups": []string{"team-red", "team-blue"}})
		mustStatus(t, code, 200, "drop a group")
		if len(arr(s, "groups")) != 2 || s["enabled"] != true {
			t.Fatalf("settings after drop: %v", s)
		}
	})

	t.Run("percentages per group without names", func(t *testing.T) {
		code, b := call(t, h, red3, "GET", board, nil)
		mustStatus(t, code, 200, "learner reads board")
		g := teamGroups(t, b)
		// team-red: 4 members (the outsider counts as a team member), 2 done.
		wantTeam(t, g["team-red"], 4, 2, 50, "team-red")
		if str(g["team-red"], "hidden_reason") != "" {
			t.Fatalf("team-red is big enough to show: %v", g["team-red"])
		}
		// team-blue has a single member: a percentage would name them.
		wantHidden(t, g["team-blue"], "team-blue")
		raw, _ := json.Marshal(b)
		for _, leak := range []string{"display_name", "user_id", "red-one", "Red One", "red-two", "Red Two"} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("board leaks %q: %s", leak, raw)
			}
		}
	})

	t.Run("small teams are hidden until they have three members", func(t *testing.T) {
		exec(t, pool, `INSERT INTO user_groups (user_id, group_name) VALUES ($1, 'team-pair'), ($2, 'team-pair')`,
			ids[red1.user], ids[blue.user])
		code, _ := call(t, h, trainerA, "PUT", settings, map[string]any{"groups": []string{"team-red", "team-blue", "team-pair"}})
		mustStatus(t, code, 200, "add team-pair")
		code, b := call(t, h, trainerA, "GET", board, nil)
		mustStatus(t, code, 200, "board with a pair")
		wantHidden(t, teamGroups(t, b)["team-pair"], "two members")

		exec(t, pool, `INSERT INTO user_groups (user_id, group_name) VALUES ($1, 'team-pair')`, ids[red2.user])
		code, b = call(t, h, trainerA, "GET", board, nil)
		mustStatus(t, code, 200, "board with a trio")
		// red1 and red2 finished the page; blue did not.
		wantTeam(t, teamGroups(t, b)["team-pair"], 3, 2, 67, "three members")

		code, _ = call(t, h, trainerA, "PUT", settings, map[string]any{"groups": []string{"team-red", "team-blue"}})
		mustStatus(t, code, 200, "drop team-pair")
	})

	t.Run("only enrolled people see it", func(t *testing.T) {
		code, _ := call(t, h, outsider, "GET", board, nil)
		mustStatus(t, code, 403, "outsider reads board")
	})

	t.Run("my teams", func(t *testing.T) {
		code, out := call(t, h, red1, "GET", "/api/v1/me/teams", nil)
		mustStatus(t, code, 200, "my teams")
		teams := arr(out, "teams")
		if len(teams) != 1 || str(obj(teams[0]), "title") != "Team course" {
			t.Fatalf("red1 teams: %v", out)
		}
		g := teamGroups(t, obj(teams[0]))
		if len(g) != 1 {
			t.Fatalf("red1 should only see team-red: %v", g)
		}
		wantTeam(t, g["team-red"], 4, 2, 50, "red1 team")
		code, out = call(t, h, outsider, "GET", "/api/v1/me/teams", nil)
		mustStatus(t, code, 200, "outsider teams")
		if len(arr(out, "teams")) != 0 {
			t.Fatalf("outsider is not enrolled: %v", out)
		}
	})

	t.Run("qualification board", func(t *testing.T) {
		code, qual := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "team-q", "name": "Team qualification"})
		mustStatus(t, code, 201, "qualification")
		qualID := str(qual, "id")
		// Linking the qualification certifies red1 and red2 (both finished);
		// red2's certificate is then made to have expired.
		setQualification(t, h, courseID, qualID)
		exec(t, pool, `UPDATE certificates SET expires_at = now() - interval '1 day' WHERE user_id = $1 AND qualification_id = $2`,
			ids[red2.user], qualID)
		qBoard := "/api/v1/qualifications/" + qualID + "/team-board"

		code, _ = call(t, h, red3, "GET", qBoard, nil)
		mustStatus(t, code, 404, "disabled qualification board")
		code, _ = call(t, h, trainerA, "PUT", qBoard+"/settings", map[string]any{"enabled": true})
		mustStatus(t, code, 403, "trainer configures qualification board")
		code, _ = call(t, h, admin, "PUT", qBoard+"/settings", map[string]any{"enabled": true, "groups": []string{"team-red"}})
		mustStatus(t, code, 200, "admin configures qualification board")

		code, b := call(t, h, red3, "GET", qBoard, nil)
		mustStatus(t, code, 200, "enrolled learner reads qualification board")
		// Only red1 holds a valid certificate; red2's expired.
		wantTeam(t, teamGroups(t, b)["team-red"], 4, 1, 25, "qualification team-red")
		code, _ = call(t, h, outsider, "GET", qBoard, nil)
		mustStatus(t, code, 404, "outsider reads qualification board")

		code, out := call(t, h, red1, "GET", "/api/v1/me/teams", nil)
		mustStatus(t, code, 200, "my teams with qualification")
		if len(arr(out, "teams")) != 2 {
			t.Fatalf("red1 teams: %v", out)
		}
	})

	t.Run("disabling hides it again", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PUT", settings, map[string]any{"enabled": false})
		mustStatus(t, code, 200, "disable")
		code, _ = call(t, h, red1, "GET", board, nil)
		mustStatus(t, code, 404, "learner reads disabled board")
		code, out := call(t, h, blue, "GET", "/api/v1/me/teams", nil)
		mustStatus(t, code, 200, "blue teams")
		if len(arr(out, "teams")) != 0 {
			t.Fatalf("blue teams after disabling: %v", out)
		}
	})
}
