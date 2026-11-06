package http_test

// In-person session tests: booking with capacity and waitlist, promotion on
// cancel, no cancel after start, QR check-in (codes, time window, walk-ins)
// and the staff roster. Same DB-backed setup as courses_test.go.

import (
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

var (
	sam = identity{user: "sam-session", groups: ""}
	kim = identity{user: "kim-session", groups: ""}
	lee = identity{user: "lee-session", groups: ""}
)

func signIn(t *testing.T, h nethttp.Handler, ids ...identity) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, id := range ids {
		code, me := call(t, h, id, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "me for "+id.user)
		out[id.user] = str(me, "id")
	}
	return out
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func sessionCompleted(t *testing.T, h nethttp.Handler, id identity, courseID, activityID string) bool {
	t.Helper()
	code, detail := call(t, h, id, "GET", "/api/v1/courses/"+courseID, nil)
	mustStatus(t, code, 200, "course detail for "+id.user)
	done, _ := findActivity(t, detail, activityID)["completed"].(bool)
	return done
}

func TestSessions(t *testing.T) {
	h := setupRouter(t)
	ids := signIn(t, h, trainerA, trainerB, sam, kim, lee)

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Fire safety briefing"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "On site"})
	mustStatus(t, code, 201, "create section")
	code, session := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"kind": "session", "title": "Walkthrough", "body": "Meet at the main entrance"})
	mustStatus(t, code, 201, "create session activity")
	if str(session, "kind") != "session" {
		t.Fatalf("activity kind = %q, want session", str(session, "kind"))
	}
	activityID := str(session, "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	for _, who := range []identity{sam, kim} {
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
			map[string]any{"user_id": ids[who.user], "role": "learner"})
		mustStatus(t, code, 201, "enrol "+who.user)
	}

	now := time.Now()
	code, slot1 := call(t, h, trainerA, "POST", "/api/v1/activities/"+activityID+"/session-slots",
		map[string]any{"starts_at": rfc(now.Add(2 * time.Hour)), "ends_at": rfc(now.Add(3 * time.Hour)), "location": "Hall A", "capacity": 1})
	mustStatus(t, code, 201, "create slot 1")
	slot1ID := str(slot1, "id")
	code, slot2 := call(t, h, trainerA, "POST", "/api/v1/activities/"+activityID+"/session-slots",
		map[string]any{"starts_at": rfc(now.Add(4 * time.Hour)), "ends_at": rfc(now.Add(5 * time.Hour)), "capacity": 5})
	mustStatus(t, code, 201, "create slot 2")
	slot2ID := str(slot2, "id")

	t.Run("slot validation and authoring rights", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/activities/"+activityID+"/session-slots",
			map[string]any{"starts_at": rfc(now.Add(time.Hour)), "ends_at": rfc(now), "capacity": 3})
		mustStatus(t, code, 422, "slot ending before it starts")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+activityID+"/session-slots",
			map[string]any{"starts_at": rfc(now), "ends_at": rfc(now.Add(time.Hour)), "capacity": 0})
		mustStatus(t, code, 422, "slot without capacity")
		code, _ = call(t, h, sam, "POST", "/api/v1/activities/"+activityID+"/session-slots",
			map[string]any{"starts_at": rfc(now), "ends_at": rfc(now.Add(time.Hour)), "capacity": 3})
		mustStatus(t, code, 403, "learner creates slot")
		code, _ = call(t, h, sam, "PUT", "/api/v1/activities/"+activityID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 400, "manual completion of a session")
	})

	t.Run("booking, waitlist and promotion", func(t *testing.T) {
		code, b := call(t, h, sam, "POST", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 201, "sam books slot 1")
		if str(b, "status") != "booked" {
			t.Fatalf("sam status = %q, want booked", str(b, "status"))
		}
		code, _ = call(t, h, sam, "POST", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 200, "sam books slot 1 again (idempotent)")

		code, b = call(t, h, kim, "POST", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 201, "kim books full slot 1")
		if str(b, "status") != "waitlist" || num(b, "waitlist_position") != 1 {
			t.Fatalf("kim booking = %v, want waitlist position 1", b)
		}
		code, info := call(t, h, kim, "GET", "/api/v1/activities/"+activityID+"/session", nil)
		mustStatus(t, code, 200, "kim reads session")
		if s := obj(arr(info, "slots")[0]); num(s, "seats_left") != 0 || num(s, "waitlist") != 1 {
			t.Fatalf("slot 1 counts = %v", s)
		}

		code, _ = call(t, h, kim, "POST", "/api/v1/slots/"+slot2ID+"/booking", nil)
		mustStatus(t, code, 409, "kim books a second time of the same session")
		code, _ = call(t, h, lee, "POST", "/api/v1/slots/"+slot2ID+"/booking", nil)
		mustStatus(t, code, 403, "not enrolled lee books")

		code, _ = call(t, h, sam, "DELETE", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 204, "sam cancels")
		code, info = call(t, h, kim, "GET", "/api/v1/activities/"+activityID+"/session", nil)
		mustStatus(t, code, 200, "kim re-reads session")
		if my := obj(info["my_signup"]); str(my, "status") != "booked" || str(my, "slot_id") != slot1ID {
			t.Fatalf("kim after promotion = %v, want booked on slot 1", my)
		}

		code, b = call(t, h, sam, "POST", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 201, "sam rebooks onto the waitlist")
		if str(b, "status") != "waitlist" {
			t.Fatalf("sam status = %q, want waitlist", str(b, "status"))
		}
		code, _ = call(t, h, sam, "DELETE", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 204, "sam leaves the waitlist")
	})

	// Slot 1 is now in progress.
	code, _ = call(t, h, trainerA, "PATCH", "/api/v1/session-slots/"+slot1ID,
		map[string]any{"starts_at": rfc(now.Add(-10 * time.Minute)), "ends_at": rfc(now.Add(50 * time.Minute))})
	mustStatus(t, code, 200, "move slot 1 to now")

	t.Run("no booking changes once started", func(t *testing.T) {
		code, _ := call(t, h, kim, "DELETE", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 409, "kim cancels after start")
		code, _ = call(t, h, sam, "POST", "/api/v1/slots/"+slot1ID+"/booking", nil)
		mustStatus(t, code, 409, "sam books a started slot")
	})

	t.Run("QR check-in", func(t *testing.T) {
		code, _ := call(t, h, trainerB, "GET", "/api/v1/slots/"+slot1ID+"/checkin-code", nil)
		mustStatus(t, code, 403, "other trainer reads the check-in code")
		code, _ = call(t, h, kim, "GET", "/api/v1/slots/"+slot1ID+"/checkin-code", nil)
		mustStatus(t, code, 403, "learner reads the check-in code")
		code, qr := call(t, h, trainerA, "GET", "/api/v1/slots/"+slot1ID+"/checkin-code", nil)
		mustStatus(t, code, 200, "staff reads the check-in code")
		checkin := str(qr, "code")
		if checkin == "" || !strings.Contains(str(qr, "url"), "/checkin/"+slot1ID+"?code="+checkin) || str(qr, "expires_at") == "" {
			t.Fatalf("check-in code payload = %v", qr)
		}

		code, _ = call(t, h, kim, "POST", "/api/v1/slots/"+slot1ID+"/checkin", map[string]any{"code": "AAAAAAAA"})
		mustStatus(t, code, 400, "wrong code")
		if sessionCompleted(t, h, kim, courseID, activityID) {
			t.Fatal("kim completed before checking in")
		}
		code, res := call(t, h, kim, "POST", "/api/v1/slots/"+slot1ID+"/checkin", map[string]any{"code": checkin})
		mustStatus(t, code, 200, "kim checks in")
		if str(obj(res["signup"]), "status") != "attended" || res["already_attended"] != false {
			t.Fatalf("kim check-in = %v", res)
		}
		if !sessionCompleted(t, h, kim, courseID, activityID) {
			t.Fatal("attending should complete the session")
		}
		code, res = call(t, h, kim, "POST", "/api/v1/slots/"+slot1ID+"/checkin", map[string]any{"code": checkin})
		mustStatus(t, code, 200, "kim scans again")
		if res["already_attended"] != true {
			t.Fatalf("second scan = %v, want already_attended", res)
		}

		code, _ = call(t, h, sam, "POST", "/api/v1/slots/"+slot1ID+"/checkin", map[string]any{"code": checkin})
		mustStatus(t, code, 409, "walk-in on a full slot")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/session-slots/"+slot1ID, map[string]any{"capacity": 2})
		mustStatus(t, code, 200, "raise capacity")
		code, res = call(t, h, sam, "POST", "/api/v1/slots/"+slot1ID+"/checkin", map[string]any{"code": checkin})
		mustStatus(t, code, 200, "sam walks in")
		if su := obj(res["signup"]); str(su, "status") != "attended" || su["walk_in"] != true {
			t.Fatalf("walk-in = %v", su)
		}

		code, _ = call(t, h, lee, "POST", "/api/v1/slots/"+slot1ID+"/checkin", map[string]any{"code": checkin})
		mustStatus(t, code, 403, "not enrolled lee checks in")

		code, qr2 := call(t, h, trainerA, "GET", "/api/v1/slots/"+slot2ID+"/checkin-code", nil)
		mustStatus(t, code, 200, "slot 2 code")
		code, _ = call(t, h, lee, "POST", "/api/v1/slots/"+slot2ID+"/checkin", map[string]any{"code": str(qr2, "code")})
		mustStatus(t, code, 403, "lee still not enrolled")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
			map[string]any{"user_id": ids[lee.user], "role": "learner"})
		mustStatus(t, code, 201, "enrol lee")
		code, _ = call(t, h, lee, "POST", "/api/v1/slots/"+slot2ID+"/checkin", map[string]any{"code": str(qr2, "code")})
		mustStatus(t, code, 409, "check-in before the window opens")
		code, _ = call(t, h, lee, "POST", "/api/v1/slots/"+slot2ID+"/checkin", map[string]any{"code": checkin})
		mustStatus(t, code, 400, "slot 1 code on slot 2")
	})

	t.Run("roster and manual attendance", func(t *testing.T) {
		code, _ := call(t, h, kim, "GET", "/api/v1/slots/"+slot1ID+"/roster", nil)
		mustStatus(t, code, 403, "learner reads roster")
		code, roster := call(t, h, trainerA, "GET", "/api/v1/slots/"+slot1ID+"/roster", nil)
		mustStatus(t, code, 200, "staff reads roster")
		signups := arr(roster, "signups")
		if len(signups) != 2 {
			t.Fatalf("roster has %d signups, want 2", len(signups))
		}
		kimSignup := ""
		for _, s := range signups {
			if str(obj(s), "user_id") == ids[kim.user] {
				kimSignup = str(obj(s), "id")
			}
		}
		if kimSignup == "" {
			t.Fatal("kim not on roster")
		}

		code, _ = call(t, h, kim, "PUT", "/api/v1/signups/"+kimSignup, map[string]any{"status": "attended"})
		mustStatus(t, code, 403, "learner marks themselves")
		code, _ = call(t, h, trainerA, "PUT", "/api/v1/signups/"+kimSignup, map[string]any{"status": "waitlist"})
		mustStatus(t, code, 422, "invalid status")

		code, _ = call(t, h, trainerA, "PUT", "/api/v1/signups/"+kimSignup, map[string]any{"status": "no_show"})
		mustStatus(t, code, 200, "mark kim no-show")
		if sessionCompleted(t, h, kim, courseID, activityID) {
			t.Fatal("no-show should remove the completion")
		}
		code, _ = call(t, h, trainerA, "PUT", "/api/v1/signups/"+kimSignup, map[string]any{"status": "attended"})
		mustStatus(t, code, 200, "mark kim attended")
		if !sessionCompleted(t, h, kim, courseID, activityID) {
			t.Fatal("attended should complete the session")
		}
	})
}
