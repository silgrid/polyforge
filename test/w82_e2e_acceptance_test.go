package test

// w82_e2e_acceptance_test.go: e2e acceptance suite for,
// authored before `code` from artifacts/spec.md only; doc artifacts/e2e-scenarios.md.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

var w82HTTP = &http.Client{Timeout: 60 * time.Second}

func w82Send(inst *v1Install, bearer string, body []byte, machine string, priv ed25519.PrivateKey, ts int64) (w57Ans, error) {
	req, err := http.NewRequest("POST", inst.url+w57Path, bytes.NewReader(body))
	if err != nil {
		return w57Ans{}, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	if priv != nil {
		if ts == 0 {
			ts = time.Now().Unix()
		}
		req.Header.Set("X-PF-Machine-ID", machine)
		req.Header.Set("X-PF-Timestamp", fmt.Sprint(ts))
		req.Header.Set("X-PF-Signature", w57Sign("POST", w57Path, body, ts, machine, priv))
	}
	resp, err := w82HTTP.Do(req)
	if err != nil {
		return w57Ans{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return w57Ans{}, err
	}
	ans := w57Ans{Status: resp.StatusCode, Raw: string(raw)}
	var typed api.Error
	if json.Unmarshal(raw, &typed) == nil {
		ans.Code, ans.Detail = typed.Code, typed.Detail
	}
	_ = json.Unmarshal(raw, &ans.Body)
	return ans, nil
}
func w82Beat(t *testing.T, inst *v1Install, bearer string, body []byte, machine string, priv ed25519.PrivateKey, ts int64) w57Ans {
	t.Helper()
	ans, err := w82Send(inst, bearer, body, machine, priv, ts)
	if err != nil {
		t.Fatalf("heartbeat %s: %v", machine, err)
	}
	return ans
}
func w82WantOK(t *testing.T, what string, ans w57Ans) {
	t.Helper()
	if ans.Status != http.StatusOK {
		t.Fatalf("%s = %s, want 200", what, ans)
	}
}
func w82Registration(t *testing.T, id, pub string) []byte {
	t.Helper()
	return w57JSON(t, map[string]any{
		"machine_id": id, "public_key": pub, "launcher": "pi-web",
		"role": "runner", "live_wi_ids": []string{},
	})
}
func w82UnsignedBeat(t *testing.T, inst *v1Install, bearer, id, version, mode string, live []string) w57Ans {
	t.Helper()
	body := w57JSON(t, map[string]any{
		"machine_id": id, "runtime_version": version, "mode": mode, "live_wi_ids": live,
	})
	return w82Beat(t, inst, bearer, body, "", nil, 0)
}
func w82RowCount(t *testing.T, inst *v1Install, id string) int {
	t.Helper()
	db := w57DB(t, inst)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id=?1`, id).Scan(&n); err != nil {
		t.Fatalf("count machines row %q: %v", id, err)
	}
	return n
}
func w82SetVersion(t *testing.T, inst *v1Install, id string, version any) {
	t.Helper()
	db := w57DB(t, inst)
	defer db.Close()
	if _, err := db.Exec(`UPDATE machines SET runtime_version=?2 WHERE id=?1`, id, version); err != nil {
		t.Fatalf("set runtime_version of %q: %v", id, err)
	}
}
func w82Liveness(t *testing.T, inst *v1Install, id string) map[string]any {
	t.Helper()
	row := w57Row(t, inst, id)
	if row == nil {
		t.Fatalf("machines row %q is absent", id)
	}
	out := map[string]any{}
	for _, k := range []string{"last_heartbeat_at", "runtime_version", "mode", "live_wi_ids"} {
		out[k] = row[k]
	}
	return out
}
func w82SameLiveness(t *testing.T, what string, before, after map[string]any) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed %s from %#v to %#v (a refused beat must write nothing)", what, k, v, after[k])
		}
	}
}

type w82Contender struct {
	userID, display, bearer, pub string
	priv                         ed25519.PrivateKey
}

func w82Race(t *testing.T, inst *v1Install, id string, contenders []w82Contender) []w57Ans {
	t.Helper()
	bodies := make([][]byte, len(contenders))
	ts := time.Now().Unix()
	for i, c := range contenders {
		bodies[i] = w57JSON(t, map[string]any{
			"machine_id": id, "public_key": c.pub, "launcher": "pi-web",
			"role": "runner", "live_wi_ids": []string{fmt.Sprintf("wi-race-%d", i)},
		})
	}
	start := make(chan struct{})
	results := make([]w57Ans, len(contenders))
	errs := make([]error, len(contenders))
	var wg sync.WaitGroup
	for i, c := range contenders {
		wg.Add(1)
		go func(i int, c w82Contender) {
			defer wg.Done()
			<-start
			results[i], errs[i] = w82Send(inst, c.bearer, bodies[i], id, c.priv, ts)
		}(i, c)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("contender %d (%s): %v", i, contenders[i].userID, err)
		}
	}
	return results
}
func w82CheckOneWinner(t *testing.T, inst *v1Install, id string, answers []w57Ans, contenders []w82Contender) {
	t.Helper()
	winner := -1
	for i, ans := range answers {
		switch ans.Status {
		case http.StatusOK:
			if winner >= 0 {
				t.Fatalf("%s has two winners: %v", id, answers)
			}
			winner = i
		case http.StatusForbidden:
			if ans.Code != api.ErrAuthForbidden {
				t.Fatalf("%s loser = %s, want %s", id, ans, api.ErrAuthForbidden)
			}
			if !strings.Contains(strings.ToLower(ans.Raw), "another operator") {
				t.Fatalf("%s loser no longer says another operator: %s", id, ans)
			}
			for _, c := range contenders {
				if strings.Contains(ans.Raw, c.userID) || strings.Contains(ans.Raw, c.display) {
					t.Fatalf("%s refusal named operator %q/%q: %s", id, c.userID, c.display, ans)
				}
			}
		default:
			t.Fatalf("%s contender %d = %s, want 200 or 403 (never 500)", id, i, ans)
		}
	}
	if winner < 0 {
		t.Fatalf("%s answered no 200: %v", id, answers)
	}
	want := contenders[winner]
	row := w57Row(t, inst, id)
	if row["operator_id"] != want.userID || row["public_key"] != want.pub {
		t.Fatalf("%s row = %v, want the winner %q key %q", id, row, want.userID, want.pub)
	}
	if n := w82RowCount(t, inst, id); n != 1 {
		t.Fatalf("%s left %d machines rows, want 1", id, n)
	}
}

// W82-S1: a captured signature survives a >4096-signature flood from other machines.
func TestW82ReplayFloodCannotEvictCapturedSignature(t *testing.T) {
	inst := newV1Install(t)
	_, _, opKey := w57User(t, inst, "w82-flood-op", "writer", "maintainer")
	mPub, mPriv := w57Key(t, 0x82)
	mBody := w82Registration(t, "w82-captured", mPub)
	mTS := time.Now().Unix()
	w82WantOK(t, "captured machine registration", w82Beat(t, inst, opKey, mBody, "w82-captured", mPriv, mTS))

	// 9 well-behaved machines push 500 each (4500 accepted entries, past the
	// old global 4096 capacity, while each stays under the plan's quota 512).
	fPub, fPriv := w57Key(t, 0x83)
	const machines, perMachine = 9, 500
	for m := 0; m < machines; m++ {
		id := fmt.Sprintf("w82-flood-%d", m)
		w82WantOK(t, "flood registration "+id, w82Beat(t, inst, opKey, w82Registration(t, id, fPub), id, fPriv, 0))
		for i := 1; i < perMachine; i++ {
			body := w57JSON(t, map[string]any{
				"machine_id": id, "live_wi_ids": []string{fmt.Sprintf("wi-%d-%d", m, i)},
			})
			w82WantOK(t, fmt.Sprintf("flood beat %s #%d", id, i), w82Beat(t, inst, opKey, body, id, fPriv, 0))
		}
	}
	replay := w82Beat(t, inst, opKey, mBody, "w82-captured", mPriv, mTS)
	if replay.Status != http.StatusUnauthorized || replay.Code != api.ErrAuthUnauthorized ||
		!strings.Contains(strings.ToLower(replay.Detail), "replay") {
		t.Fatalf("captured signature after %d accepted signatures from other machines = %s, want 401 %s naming a replay",
			machines*perMachine, replay, api.ErrAuthUnauthorized)
	}
}

// W82-S2: a saturated machine gets 429 while another machine is accepted.
func TestW82ReplayQuotaIsolatesEachMachine(t *testing.T) {
	inst := newV1Install(t)
	_, _, opKey := w57User(t, inst, "w82-quota-op", "writer", "maintainer")
	pubA, privA := w57Key(t, 0x84)
	const idA = "w82-quota-a"
	// Plan D1 pins the quota at 512 live entries; the registration is the first.
	w82WantOK(t, "machine A registration", w82Beat(t, inst, opKey, w82Registration(t, idA, pubA), idA, privA, 0))
	for i := 1; i <= 511; i++ {
		body := w57JSON(t, map[string]any{"machine_id": idA, "live_wi_ids": []string{fmt.Sprintf("wi-a-%d", i)}})
		w82WantOK(t, fmt.Sprintf("machine A beat #%d", i), w82Beat(t, inst, opKey, body, idA, privA, 0))
	}
	over := w57JSON(t, map[string]any{"machine_id": idA, "live_wi_ids": []string{"wi-a-over"}})
	refused := w82Beat(t, inst, opKey, over, idA, privA, 0)
	if refused.Status != http.StatusTooManyRequests || refused.Code != api.ErrCoreTooMany {
		t.Fatalf("machine A's 513th fresh signed request = %s, want 429 %s", refused, api.ErrCoreTooMany)
	}
	pubB, privB := w57Key(t, 0x85)
	const idB = "w82-quota-b"
	w82WantOK(t, "machine B registration", w82Beat(t, inst, opKey, w82Registration(t, idB, pubB), idB, privB, 0))
	bBody := w57JSON(t, map[string]any{"machine_id": idB, "live_wi_ids": []string{"wi-b-1"}})
	w82WantOK(t, "machine B while A is over quota", w82Beat(t, inst, opKey, bBody, idB, privB, 0))
}

// W82-S3: concurrent registrations never 500 and leave one row; the loser
// gets the sequential outcome (same operator 200 refresh, different 403 unnamed).
func TestW82ConcurrentRegistrationKeepsOneRowAndOwner(t *testing.T) {
	inst := newV1Install(t)
	u1, _, k1 := w57User(t, inst, "w82-reg-same", "writer", "maintainer")
	u2, _, k2 := w57User(t, inst, "w82-reg-other", "writer", "maintainer")
	pub1, priv1 := w57Key(t, 0x86)
	pub2, priv2 := w57Key(t, 0x87)
	c1 := w82Contender{userID: u1, display: "w82-reg-same", bearer: k1, pub: pub1, priv: priv1}
	c2 := w82Contender{userID: u2, display: "w82-reg-other", bearer: k2, pub: pub2, priv: priv2}

	for round := 0; round < 5; round++ {
		id := fmt.Sprintf("w82-same-%d", round)
		answers := w82Race(t, inst, id, []w82Contender{c1, c1, c1, c1})
		registered := 0
		for i, ans := range answers {
			if ans.Status != http.StatusOK {
				t.Fatalf("same-operator race %s contender %d = %s, want 200", id, i, ans)
			}
			if got, _ := ans.Body["registered"].(bool); got {
				registered++
			}
		}
		if registered != 1 {
			t.Fatalf("same-operator race %s answered %d registrations, want one: %v", id, registered, answers)
		}
		row := w57Row(t, inst, id)
		if row["operator_id"] != u1 || row["public_key"] != pub1 {
			t.Fatalf("same-operator race %s row = %v, want operator %q key %q", id, row, u1, pub1)
		}
		if n := w82RowCount(t, inst, id); n != 1 {
			t.Fatalf("same-operator race %s left %d machines rows, want 1", id, n)
		}
	}
	for round := 0; round < 8; round++ {
		id := fmt.Sprintf("w82-diff-%d", round)
		w82CheckOneWinner(t, inst, id, w82Race(t, inst, id, []w82Contender{c1, c2}), []w82Contender{c1, c2})
	}
}

// W82-S4: concurrent claims of a legacy NULL-operator row leave exactly one
// owner, the losers a 403 that rebinds nothing.
func TestW82ConcurrentLegacyClaimKeepsOneOwner(t *testing.T) {
	inst := newV1Install(t)
	contenders := make([]w82Contender, 4)
	for i := range contenders {
		uid, _, key := w57User(t, inst, fmt.Sprintf("w82-claim-op-%d", i), "writer", "maintainer")
		pub, priv := w57Key(t, byte(0x90+i))
		contenders[i] = w82Contender{userID: uid, display: fmt.Sprintf("w82-claim-op-%d", i), bearer: key, pub: pub, priv: priv}
	}
	for round := 0; round < 5; round++ {
		id := fmt.Sprintf("w82-claim-%d", round)
		w57SeedRow(t, inst, id, "", "")
		w82CheckOneWinner(t, inst, id, w82Race(t, inst, id, contenders), contenders)
	}
}

// W82-S5: an unsigned beat is accepted only when the request and the stored
// version are both pre-v2.20.8, fails closed on dev/empty/unparseable, and a
// signed v2.20.8+ refresh ratchets the row closed.
func TestW82UnsignedTransitionIsVersionGatedAndRatcheted(t *testing.T) {
	inst := newV1Install(t)
	uid, _, opKey := w57User(t, inst, "w82-unsigned-op", "writer", "maintainer")
	pub, priv := w57Key(t, 0x95)
	const id = "w82-unsigned-row"
	w57SeedRow(t, inst, id, uid, pub)
	w82SetVersion(t, inst, id, "v2.20.6")

	w82WantOK(t, "pre-signing unsigned beat", w82UnsignedBeat(t, inst, opKey, id, "v2.20.6", "run", []string{"wi-pre"}))
	row := w57Row(t, inst, id)
	if row["runtime_version"] != "v2.20.6" || row["mode"] != "run" {
		t.Fatalf("pre-signing unsigned beat did not refresh liveness: %v", row)
	}
	before := w82Liveness(t, inst, id)
	for _, version := range []string{"v2.20.8", "v2.20.9", "dev", "", "garbage"} {
		ans := w82UnsignedBeat(t, inst, opKey, id, version, "bad", []string{"wi-bad"})
		if ans.Status != http.StatusUnauthorized || ans.Code != api.ErrAuthUnauthorized {
			t.Fatalf("unsigned request version %q on a pre-v2.20.8 row = %s, want 401 %s", version, ans, api.ErrAuthUnauthorized)
		}
		w82SameLiveness(t, fmt.Sprintf("refused unsigned beat with version %q", version), before, w82Liveness(t, inst, id))
	}

	signed := w57JSON(t, map[string]any{
		"machine_id": id, "runtime_version": "v2.20.9", "mode": "signed", "live_wi_ids": []string{"wi-signed"},
	})
	w82WantOK(t, "signed v2.20.9 beat", w82Beat(t, inst, opKey, signed, id, priv, 0))
	if got := w57Row(t, inst, id)["runtime_version"]; got != "v2.20.9" {
		t.Fatalf("signed beat stored runtime_version %v, want v2.20.9", got)
	}
	before = w82Liveness(t, inst, id)
	ans := w82UnsignedBeat(t, inst, opKey, id, "v2.20.6", "old", []string{"wi-old"})
	if ans.Status != http.StatusUnauthorized || ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("unsigned beat after a signed v2.20.9 refresh = %s, want 401 %s", ans, api.ErrAuthUnauthorized)
	}
	w82SameLiveness(t, "ratcheted unsigned beat", before, w82Liveness(t, inst, id))

	// Fail closed on the stored side too: an unparseable stored version.
	const devID = "w82-unsigned-stored-dev"
	w57SeedRow(t, inst, devID, uid, pub)
	w82SetVersion(t, inst, devID, "dev")
	before = w82Liveness(t, inst, devID)
	ans = w82UnsignedBeat(t, inst, opKey, devID, "v2.20.6", "run", []string{"wi-x"})
	if ans.Status != http.StatusUnauthorized || ans.Code != api.ErrAuthUnauthorized {
		t.Fatalf("unsigned beat on a row with stored version dev = %s, want 401 %s", ans, api.ErrAuthUnauthorized)
	}
	w82SameLiveness(t, "stored-version dev", before, w82Liveness(t, inst, devID))
}

// W82-S6: a PATCH records the authenticated user as the wi_content_updated
// actor, visible through GET ?include=events, with the payload unchanged.
func TestW82PatchRecordsRequestingUserAsActor(t *testing.T) {
	inst := newV1Install(t)
	uid, _, key := w57User(t, inst, "w82-patch-user", "writer", "writer")
	wi := w74NewWI(t, inst, inst.client, v1Project, "w82 patch actor", nil)
	if _, err := aihubclient.New(inst.url, key).PatchWorkItem(wi, map[string]any{"priority": api.PriorityHigh}); err != nil {
		t.Fatalf("PATCH work item %s: %v", wi, err)
	}
	status, raw := w74Raw(t, inst, key, "GET", "/api/v1/work-items/"+wi+"?include=events", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET work item events = %d %s", status, raw)
	}
	var view struct {
		Attrs struct {
			Events []map[string]any `json:"events"`
		} `json:"attrs"`
	}
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode work item events: %v (%s)", err, raw)
	}
	found := false
	for _, ev := range view.Attrs.Events {
		if ev["type"] != "wi_content_updated" {
			continue
		}
		found = true
		if ev["actor"] != uid {
			t.Fatalf("wi_content_updated actor = %#v, want the requesting user %q (event %v)", ev["actor"], uid, ev)
		}
		if payload, _ := ev["payload"].(map[string]any); len(payload) != 0 {
			t.Fatalf("wi_content_updated payload changed: %v", ev["payload"])
		}
	}
	if !found {
		t.Fatalf("no wi_content_updated event in %s", raw)
	}
}
