package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type env struct {
	t    *testing.T
	home string // the record (COACH_HOME)
	ws   string // the workspace around it
	now  time.Time
}

// newEnv isolates a test: its own config path, record and timezone, and no
// integration keys unless the test sets them.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	home := filepath.Join(ws, "coaching")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COACHCTL_CONFIG", filepath.Join(dir, "env"))
	t.Setenv("COACH_HOME", home)
	t.Setenv("COACH_TIMEZONE", "Europe/Berlin")
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	for _, k := range []string{"INTERVALS_API_KEY", "INTERVALS_ATHLETE_ID", "INTERVALS_API_BASE", "HEVY_API_KEY", "HEVY_API_BASE", "TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID", "TELEGRAM_API_BASE", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	return &env{t: t, home: home, ws: ws, now: time.Date(2026, 10, 17, 9, 30, 0, 0, loc)}
}

func (e *env) run(stdin string, args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	app := &App{Version: "test", Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb, Now: func() time.Time { return e.now }}
	code = app.Run(args)
	return out.String(), errb.String(), code
}

func (e *env) ok(stdin string, args ...string) string {
	e.t.Helper()
	out, errOut, code := e.run(stdin, args...)
	if code != 0 {
		e.t.Fatalf("coachctl %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func TestExitCodes(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("", "no-such-command"); code != ExitUsage {
		t.Errorf("unknown command: exit %d", code)
	}
	if _, _, code := e.run("", "journal", "show", "abc"); code != ExitUsage {
		t.Errorf("bad id: exit %d", code)
	}
	if _, _, code := e.run("", "journal", "show", "5"); code != ExitUsage {
		t.Errorf("unknown id: exit %d", code)
	}
	if _, _, code := e.run("", "item", "add", "x"); code != ExitUsage {
		t.Errorf("missing --kind: exit %d", code)
	}
	if _, errOut, code := e.run("", "icu", "profile"); code != ExitUsage || !strings.Contains(errOut, "INTERVALS_API_KEY") {
		t.Errorf("unconfigured icu: exit %d, %q", code, errOut)
	}
	t.Setenv("COACH_HOME", filepath.Join(e.ws, "missing"))
	if _, errOut, code := e.run("", "session", "start"); code != ExitUsage || !strings.Contains(errOut, "COACH_HOME") {
		t.Errorf("missing record: exit %d, %q", code, errOut)
	}
}

func TestSessionStart(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.home, "main.md"), []byte("# Main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.home, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.home, "references", "injuries.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.ok("Session one\nmore", "journal", "add")
	e.ok(strings.Repeat("long ", 400), "journal", "add")
	e.ok("", "item", "add", "--kind", "commitment", "--date", "2026-10-16", "Overdue thing")
	e.ok("", "item", "add", "--kind", "flag", "Fresh flag")
	e.ok("", "metric", "define", "ftp", "--kind", "state", "--unit", "W", "--stale-after", "10")
	e.ok("", "metric", "record", "ftp", "260", "--at", "2026-09-01")

	out := e.ok("", "session", "start")
	for _, want := range []string{
		"Today: Sat 2026-10-17, 09:30 (Europe/Berlin)",
		"⚠ Stale state metrics: ftp = 260 W (measured 2026-09-01, stale after 10 d)",
		"## Open items (2 open, 1 OVERDUE)",
		"#1 [commitment] (opened 2026-10-17, for 2026-10-16 — OVERDUE) Overdue thing",
		"(+799 chars — coachctl journal show 2)",
		"#1 [2026-10-17 09:30] Session one\nmore",
		"main.md (7 B)",
		"references/: injuries",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("session start lacks %q\n%s", want, out)
		}
	}
	if strings.Index(out, "Overdue thing") > strings.Index(out, "Fresh flag") {
		t.Error("overdue items come first")
	}

	items := e.ok("", "session", "start", "--scope", "items")
	if strings.Contains(items, "## Journal") || strings.Contains(items, "Record files") || !strings.Contains(items, "Stale state metrics") {
		t.Errorf("--scope items:\n%s", items)
	}
	var js sessionOut
	if err := json.Unmarshal([]byte(e.ok("", "session", "start", "--json")), &js); err != nil {
		t.Fatal(err)
	}
	if js.Today != "2026-10-17" || js.Overdue != 1 || len(js.Journal) != 2 || js.Main == nil {
		t.Errorf("json = %+v", js)
	}
}

func TestJournalListFilters(t *testing.T) {
	e := newEnv(t)
	e.ok("SESSION-DEBRIEF 123 easy run", "journal", "add")
	e.ok("weekly review", "journal", "add")
	out := e.ok("", "journal", "list", "--grep", "debrief 123")
	if !strings.Contains(out, "#1") || strings.Contains(out, "#2") {
		t.Errorf("grep:\n%s", out)
	}
	if out := e.ok("", "journal", "list", "--since", "2026-10-18"); !strings.Contains(out, "No journal entries match") {
		t.Errorf("since:\n%s", out)
	}
	e.ok("", "journal", "archive", "1")
	if out := e.ok("", "journal", "list", "--headlines"); strings.Contains(out, "#1") {
		t.Errorf("archived entry listed without --archived:\n%s", out)
	}
	if out := e.ok("", "journal", "show", "1"); !strings.Contains(out, "[archived]") {
		t.Errorf("show archived:\n%s", out)
	}
}

func TestMetricCommands(t *testing.T) {
	e := newEnv(t)
	if _, errOut, code := e.run("", "metric", "record", "weight", "71"); code != ExitUsage || !strings.Contains(errOut, "coachctl metric define weight") {
		t.Errorf("undefined series: %d %q", code, errOut)
	}
	e.ok("", "metric", "define", "weight", "--kind", "event", "--unit", "kg")
	e.ok("", "metric", "record", "weight", "71,5", "--at", "2026-10-10")
	e.ok("", "metric", "record", "weight", "70.5")
	out := e.ok("", "metric", "get", "weight")
	if !strings.Contains(out, "weight, 2 points, min 70.5 / avg 71 / max 71.5 kg") {
		t.Errorf("get:\n%s", out)
	}
	if out := e.ok("", "metric", "list"); !strings.Contains(out, "weight [event]: 2 points, 2026-10-10 → 2026-10-17, latest 70.5 kg") {
		t.Errorf("list:\n%s", out)
	}
	if out := e.ok("", "metric", "delete", "1"); !strings.Contains(out, "Metric #1 deleted from weight") {
		t.Errorf("delete:\n%s", out)
	}
}

// fakeICU serves the handful of endpoints the commands use, checking auth.
func fakeICU(t *testing.T, posted *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "API_KEY" || pass != "k3y" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/athlete/i42" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"id":"i42","name":"Test Athlete","timezone":"America/New_York","icu_weight":70,"icu_resting_hr":48,"icu_api_key":"SECRET","email":"a@example.com",
				"sportSettings":[{"types":["Ride","VirtualRide"],"ftp":250,"lthr":165,"max_hr":185,"power_zones":[55,75,90,105,120,150,999]},
				{"types":["Run"],"threshold_pace":4.0,"pace_units":"MINS_KM","lthr":170}]}`)
		case r.URL.Path == "/athlete/i42/activities":
			if r.URL.Query().Get("oldest") != "2026-10-11" || r.URL.Query().Get("newest") != "2026-10-17" {
				t.Errorf("activities range = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `[{"id":"i2","start_date_local":"2026-10-16T07:00:00","type":"Run","name":"Easy, \"short\"","distance":10000,"moving_time":3000,"icu_training_load":50,"icu_intensity":70.5,"average_heartrate":140},
				{"id":"i1","start_date_local":"2026-10-14T18:00:00","type":"Ride","name":"Z2","distance":40000,"moving_time":5400,"icu_training_load":80}]`)
		case r.URL.Path == "/athlete/i42/events" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(posted)
			_, _ = io.WriteString(w, `{"id":777,"category":"WORKOUT","start_date_local":"2026-10-21T00:00:00","type":"Run","name":"6x1k","workout_doc":{"steps":[{},{},{}]}}`)
		case r.URL.Path == "/athlete/i42/events.json" && r.Method == http.MethodGet:
			// The lookup window spans 60 days before today to 240 after the date.
			if r.URL.Query().Get("oldest") != "2026-08-18" || r.URL.Query().Get("newest") != "2027-06-18" {
				t.Errorf("external-id lookup window = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `[{"id":778,"category":"WORKOUT","start_date_local":"2026-10-20T00:00:00","name":"old","external_id":"w43-tue"}]`)
		case r.URL.Path == "/athlete/i42/events/778" && r.Method == http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(posted)
			_, _ = io.WriteString(w, `{"id":778,"category":"WORKOUT","start_date_local":"2026-10-21T00:00:00","type":"Run","name":"6x1k","external_id":"w43-tue","workout_doc":{"steps":[{}]}}`)
		case r.URL.Path == "/athlete/i42/gear.json":
			_, _ = io.WriteString(w, `[{"id":"g1","name":"Shoe","api_key":"nope","nested":{"refresh_token":"x"}}]`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestICU(t *testing.T) {
	e := newEnv(t)
	var posted map[string]any
	srv := fakeICU(t, &posted)
	defer srv.Close()
	t.Setenv("INTERVALS_API_BASE", srv.URL)
	t.Setenv("INTERVALS_ATHLETE_ID", "i42")
	t.Setenv("INTERVALS_API_KEY", "k3y")

	out := e.ok("", "icu", "profile")
	for _, want := range []string{"Today (athlete timezone): Sat 2026-10-17", "Ride/VirtualRide: FTP 250 W, LTHR 165 bpm, max HR 185 bpm", "Run: threshold pace 4:10/km, LTHR 170 bpm", "power zones, % of FTP: 55 75 90 105 120 150 999"} {
		if !strings.Contains(out, want) {
			t.Errorf("profile lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(e.ok("", "icu", "profile", "--json"), "SECRET") {
		t.Error("profile leaked the API key")
	}

	csvOut := e.ok("", "icu", "activities", "--csv")
	if !strings.HasPrefix(csvOut, "id,start_date_local,type,name,") || !strings.Contains(csvOut, `"Easy, ""short"""`) {
		t.Errorf("csv:\n%s", csvOut)
	}
	if strings.Index(csvOut, "i1,") > strings.Index(csvOut, "i2,") {
		t.Error("activities sort oldest first")
	}
	text := e.ok("", "icu", "activities", "--type", "run")
	if !strings.Contains(text, "10.0 km  0:50:00  load 50  IF 70%  HR 140") || strings.Contains(text, "Z2") {
		t.Errorf("text:\n%s", text)
	}

	dry := e.ok("- 10m Z2", "icu", "workout", "push", "--date", "2026-10-21", "--type", "Run", "--name", "6x1k", "--dry-run")
	if !strings.Contains(dry, "Dry run, nothing sent") || posted != nil {
		t.Errorf("dry run sent or misprinted:\n%s", dry)
	}
	pushed := e.ok("- 10m Z2\n", "icu", "workout", "push", "--date", "2026-10-21", "--type", "Run", "--name", "6x1k")
	if !strings.Contains(pushed, "Created event 777") || !strings.Contains(pushed, "Parsed 3 top-level steps") {
		t.Errorf("push:\n%s", pushed)
	}
	if posted["start_date_local"] != "2026-10-21T00:00:00" || posted["description"] != "- 10m Z2" || posted["category"] != "WORKOUT" {
		t.Errorf("posted = %+v", posted)
	}
	posted = nil
	updated := e.ok("- 10m Z2\n", "icu", "workout", "push", "--date", "2026-10-21", "--type", "Run", "--name", "6x1k", "--external-id", "w43-tue")
	if !strings.Contains(updated, "Updated event 778") || posted["external_id"] != "w43-tue" || posted["start_date_local"] != "2026-10-21T00:00:00" {
		t.Errorf("a push with a known external id must update that event:\n%s\nsent %+v", updated, posted)
	}

	raw := e.ok("", "icu", "get", "/athlete/{athlete}/gear.json")
	if strings.Contains(raw, "nope") || strings.Contains(raw, `"x"`) || !strings.Contains(raw, "[redacted]") {
		t.Errorf("raw get must redact:\n%s", raw)
	}

	t.Setenv("INTERVALS_API_KEY", "wrong")
	if _, errOut, code := e.run("", "icu", "profile"); code != ExitUpstream || !strings.Contains(errOut, "INTERVALS_API_KEY") {
		t.Errorf("rejected key: exit %d, %q", code, errOut)
	}
}

func TestHevy(t *testing.T) {
	e := newEnv(t)
	var created map[string]any
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "hk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/exercise_templates":
			pages++
			if r.URL.Query().Get("page") == "1" {
				_, _ = io.WriteString(w, `{"page":1,"page_count":2,"exercise_templates":[{"id":"AAAA1111","title":"Squat (Barbell)","type":"weight_reps","primary_muscle_group":"quadriceps","equipment":"barbell"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"page":2,"page_count":2,"exercise_templates":[{"id":"BBBB2222","title":"Bulgarian Split Squat","type":"weight_reps","primary_muscle_group":"quadriceps","equipment":"dumbbell","is_custom":true}]}`)
			}
		case r.URL.Path == "/exercise_history/BBBB2222":
			_, _ = io.WriteString(w, `{"exercise_history":[{"workout_id":"w1","workout_title":"Kraft A","workout_start_time":"2026-10-10T17:00:00Z","weight_kg":20,"reps":10,"type":"normal"},{"workout_id":"w1","workout_title":"Kraft A","workout_start_time":"2026-10-10T17:00:00Z","weight_kg":20,"reps":9,"rpe":9,"type":"normal"}]}`)
		case r.URL.Path == "/routines" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = io.WriteString(w, `{"routine":[{"id":"r-1","title":"Kraft A (KW43)"}]}`)
		case r.URL.Path == "/routines/r-1" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"routine":{"id":"r-1","title":"Kraft A","folder_id":7,"exercises":[{"index":0,"title":"Bulgarian Split Squat","exercise_template_id":"BBBB2222","sets":[{"index":0,"type":"normal","weight_kg":20,"reps":null,"rep_range":{"start":8,"end":10}}]}]}}`)
		case r.URL.Path == "/routines/r-1" && r.Method == http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = io.WriteString(w, `{"routine":[{"id":"r-1","title":"Kraft A v2"}]}`)
		case r.URL.Path == "/workouts" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = io.WriteString(w, `{"workout":[{"id":"w-9","title":"Kraft A"}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("HEVY_API_BASE", srv.URL)
	t.Setenv("HEVY_API_KEY", "hk")

	out := e.ok("", "hevy", "templates", "search", "split", "squat")
	if !strings.Contains(out, "BBBB2222  Bulgarian Split Squat (custom)") || strings.Contains(out, "AAAA1111") {
		t.Errorf("search:\n%s", out)
	}
	e.ok("", "hevy", "templates", "search", "squat")
	if pages != 2 {
		t.Errorf("the catalog should be fetched once and cached, fetched %d pages", pages)
	}
	hist := e.ok("", "hevy", "history", "bulgarian split squat")
	if !strings.Contains(hist, "2026-10-10  Kraft A: 20 kg × 10, 20 kg × 9 RPE 9") {
		t.Errorf("history:\n%s", hist)
	}
	if _, errOut, code := e.run("", "hevy", "history", "squat"); code != ExitUsage || !strings.Contains(errOut, "AAAA1111") {
		t.Errorf("ambiguous title: %d %q", code, errOut)
	}

	bad := `{"title":"x","exercises":[{"exercise_template_id":"AAAA1111","sets":[{"type":"normal","rpe":8}]}]}`
	if _, errOut, code := e.run(bad, "hevy", "routine", "create"); code != ExitUsage || !strings.Contains(errOut, "rep_range, not rpe") {
		t.Errorf("rpe in routine: %d %q", code, errOut)
	}
	good := `{"title":"Kraft A (KW43)","exercises":[{"exercise_template_id":"BBBB2222","rest_seconds":90,"sets":[{"type":"normal","weight_kg":20,"rep_range":{"start":8,"end":10}}]}]}`
	if out := e.ok(good, "hevy", "routine", "create"); !strings.Contains(out, "Created routine r-1") {
		t.Errorf("create:\n%s", out)
	}
	if r, _ := created["routine"].(map[string]any); r["title"] != "Kraft A (KW43)" {
		t.Errorf("created body = %+v", created)
	}

	if out := e.ok("", "hevy", "routine", "show", "r-1"); !strings.Contains(out, "Bulgarian Split Squat [BBBB2222]: 20 kg (8-10 reps)") {
		t.Errorf("routine show:\n%s", out)
	}
	shown := e.ok("", "hevy", "routine", "show", "r-1", "--json")
	edited := strings.Replace(shown, `"Kraft A"`, `"Kraft A v2"`, 1)
	edited = strings.Replace(edited, `"rep_range": {`, `"rep_range_was": {`, 1)
	edited = strings.Replace(edited, `"rep_range_was": {`, `"rep_range": null, "x": {`, 1)
	created = nil
	if out := e.ok(edited, "hevy", "routine", "update", "r-1"); !strings.Contains(out, "Updated routine r-1") {
		t.Errorf("routine update:\n%s", out)
	}
	sent, _ := created["routine"].(map[string]any)
	ex := sent["exercises"].([]any)[0].(map[string]any)
	set := ex["sets"].([]any)[0].(map[string]any)
	if _, has := sent["id"]; has || ex["title"] != nil || ex["index"] != nil || set["index"] != nil {
		t.Errorf("update must strip fields the API does not accept back: %+v", sent)
	}
	if _, has := set["rep_range"]; has || sent["title"] != "Kraft A v2" {
		t.Errorf("a null rep_range must be dropped on update: %+v", set)
	}

	workout := `{"title":"Kraft A","start_time":"2026-10-17T17:00:00Z","end_time":"2026-10-17T17:50:00Z","exercises":[{"exercise_template_id":"BBBB2222","sets":[{"type":"normal","weight_kg":20,"reps":10,"rpe":8}]}]}`
	if _, errOut, code := e.run(`{"title":"x"}`, "hevy", "workout", "create"); code != ExitUsage || !strings.Contains(errOut, "start_time") {
		t.Errorf("incomplete workout: %d %q", code, errOut)
	}
	if out := e.ok(workout, "hevy", "workout", "create"); !strings.Contains(out, "Logged workout w-9") {
		t.Errorf("workout create:\n%s", out)
	}
}

func TestNotify(t *testing.T) {
	e := newEnv(t)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botT0K/sendMessage" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"ok":false,"description":"Not Found"}`)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":5}}`)
	}))
	defer srv.Close()
	t.Setenv("TELEGRAM_API_BASE", srv.URL)
	t.Setenv("TELEGRAM_CHAT_ID", "1001")
	t.Setenv("TELEGRAM_BOT_TOKEN", "T0K")
	if out := e.ok("Readiness: alles gut\n", "notify"); !strings.Contains(out, "Sent (20 characters)") {
		t.Errorf("notify:\n%s", out)
	}
	if got["chat_id"] != "1001" || got["text"] != "Readiness: alles gut" {
		t.Errorf("sent = %+v", got)
	}
	if _, errOut, code := e.run(strings.Repeat("ä", 4097), "notify"); code != ExitUsage || !strings.Contains(errOut, "4097 characters") {
		t.Errorf("over-long: %d %q", code, errOut)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "bad")
	if _, errOut, code := e.run("x", "notify"); code != ExitUpstream || strings.Contains(errOut, "bad") {
		t.Errorf("bad token must fail upstream without echoing it: %d %q", code, errOut)
	}
}

func TestDoctor(t *testing.T) {
	e := newEnv(t)
	cfgPath := os.Getenv("COACHCTL_CONFIG")
	if err := os.WriteFile(cfgPath, []byte("COACH_TIMEZONE=Europe/Berlin\nHEVY_API_KEY=supersecret\nTYPO_KEY=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := e.run("", "doctor")
	if code != ExitFailure || !strings.Contains(out, "chmod 600") || !strings.Contains(out, "unknown keys") || strings.Contains(out, "supersecret") {
		t.Errorf("doctor with a 0644 env file: exit %d\n%s", code, out)
	}
	if err := os.Chmod(cfgPath, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code = e.run("", "doctor")
	if code != ExitOK || !strings.Contains(out, "hevy         configured (HEVY_API_KEY set)") || !strings.Contains(out, "intervals    not configured") {
		t.Errorf("doctor: exit %d\n%s", code, out)
	}
}

func TestInitSkillAndImportStarter(t *testing.T) {
	e := newEnv(t)
	ws := filepath.Join(t.TempDir(), "new-ws")
	out := e.ok("", "init", ws)
	if !strings.Contains(out, "COACH_HOME="+filepath.Join(ws, "coaching")) {
		t.Errorf("init:\n%s", out)
	}
	for _, f := range []string{"CLAUDE.md", ".gitignore", ".claude/settings.json", "coaching/main.md", "coaching/metrics/series.toml", "scripts/run_routine.sh", "scripts/recipes/README.md"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
			t.Errorf("init did not write %s", f)
		}
	}
	if info, _ := os.Stat(filepath.Join(ws, "scripts/run_routine.sh")); info == nil || info.Mode()&0o111 == 0 {
		t.Error("run_routine.sh must be executable")
	}
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := e.ok("", "init", ws); !strings.Contains(out, "kept CLAUDE.md") {
		t.Errorf("re-init must keep files:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md")); string(data) != "mine" {
		t.Error("re-init overwrote a file")
	}

	skill := filepath.Join(t.TempDir(), "skill")
	e.ok("", "skill", "install", "--dir", skill)
	if _, err := os.Stat(filepath.Join(skill, "SKILL.md")); err != nil {
		t.Error("skill install did not write SKILL.md")
	}
	if out := e.ok("", "doctor", "--skill-dir", skill); !strings.Contains(out, "matches the binary") {
		t.Errorf("doctor skill check:\n%s", out)
	}

	t.Setenv("COACH_HOME", filepath.Join(ws, "coaching"))
	if out := e.ok("", "session", "start", "--scope", "context"); !strings.Contains(out, "main.md (") {
		t.Errorf("session on a fresh workspace:\n%s", out)
	}
	if out := e.ok("", "setup"); !strings.Contains(out, "coachctl init") {
		t.Errorf("setup guide:\n%s", out)
	}
}
