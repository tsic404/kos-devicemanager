// Package devicet11 verifies the read-only boundary at the HTTP surface: both
// device-page read routes refuse write methods with the control-plane error
// envelope and the page read-only code before any session is resolved, while
// reads keep their success shape and the projection stays unchanged
// (DEVICE-AC-7, DEVICE-TC-11, KOSD-AC-81, KOSD-TC-181).
package devicet11

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

const (
	ownerName     = "owner"
	ownerPassword = "owner-password"
)

// response is one answer of the composed control-plane surface.
type response struct {
	status int
	header http.Header
	body   []byte
}

// call sends one request to the composed surface with an optional session.
func call(t *testing.T, base, method, path, token string) response {
	t.Helper()
	request, err := http.NewRequest(method, base+path, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	answer, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer answer.Body.Close()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return response{status: answer.StatusCode, header: answer.Header.Clone(), body: body}
}

// decode decodes one JSON object.
func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return decoded
}

// failure reads the error envelope from one response (KOSD-C-12).
func failure(t *testing.T, answer response) map[string]any {
	t.Helper()
	carried, ok := decode(t, answer.body)["error"].(map[string]any)
	if !ok {
		t.Fatalf("answer carries no error object: %q", answer.body)
	}
	return carried
}

// surface composes the page shell behind the account middleware with the
// refusal mounted in front, as the control plane serves it: a page write is
// refused before the session is resolved (KOSD-C-89).
func surface(t *testing.T, cluster *harness.Cluster, now func() time.Time) (*httptest.Server, string) {
	t.Helper()
	assessment := harness.Install(t, harness.Component{
		Name:        "kos-devicemanager",
		Declaration: devicemanager.Declaration(),
	})
	shell := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)

	store, err := identity.Open(context.Background(), identity.OpenOptions{
		Path: filepath.Join(t.TempDir(), "kos.db"),
		Now:  now,
	})
	if err != nil {
		t.Fatalf("open the account store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	accounts := identity.New(store, identity.Config{Now: now})
	bootstrap, err := accounts.BootstrapOwner(context.Background(), ownerName)
	if err != nil {
		t.Fatalf("bootstrap the owner: %v", err)
	}
	login, err := accounts.Login(context.Background(), ownerName, bootstrap.OneTimePassword)
	if err != nil {
		t.Fatalf("sign in the owner: %v", err)
	}
	if err := accounts.ChangePassword(context.Background(), login.Token, bootstrap.OneTimePassword, ownerPassword); err != nil {
		t.Fatalf("replace the one-time password: %v", err)
	}

	pageAPI := pages.NewAPI(shell)
	handler := pageAPI.RefuseWrites(identity.NewAPI(accounts).Handler(pageAPI.Handler()))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, login.Token
}

// TestDevicePageRefusesWriteMethods reads the device pages, sends every write
// method to both page read routes with and without a session, and compares the
// pages and the cluster objects with their state before the writes.
func TestDevicePageRefusesWriteMethods(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster := harness.Start()
	cluster.Node(t, "node-a")
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "G-1", Model: "A100", Kind: "gpu"})

	readAt := collected.Add(30 * time.Second)
	now := func() time.Time { return readAt }
	server, token := surface(t, cluster, now)

	readPaths := []string{"/api/v1/pages", "/api/v1/pages/" + devicemanager.PageDevices}
	before := map[string]map[string]any{}
	for _, path := range readPaths {
		answer := call(t, server.URL, http.MethodGet, path, token)
		if answer.status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, answer.status, answer.body)
		}
		before[path] = decode(t, answer.body)
	}
	assertReadShape(t, before["/api/v1/pages/"+devicemanager.PageDevices])
	objects := cluster.Snapshot(t)

	for _, path := range readPaths {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			anonymous := call(t, server.URL, method, path, "")
			authenticated := call(t, server.URL, method, path, token)
			for name, answer := range map[string]response{"unauthenticated": anonymous, "authenticated": authenticated} {
				assertRefusal(t, name, method, path, answer)
			}
			if !bytes.Equal(anonymous.body, authenticated.body) {
				t.Fatalf("%s %s answer changed with the session:\n%s\n%s", method, path, anonymous.body, authenticated.body)
			}
		}
	}

	for path, want := range before {
		answer := call(t, server.URL, http.MethodGet, path, token)
		if answer.status != http.StatusOK {
			t.Fatalf("GET %s after the refused writes = %d %s", path, answer.status, answer.body)
		}
		if got := decode(t, answer.body); !reflect.DeepEqual(got, want) {
			t.Fatalf("the page projection at %s changed after the refused writes", path)
		}
	}
	if !harness.SameSnapshot(cluster.Snapshot(t), objects) {
		t.Fatal("the cluster objects or markers changed after the refused writes")
	}

	anonymousRead := call(t, server.URL, http.MethodGet, readPaths[1], "")
	if anonymousRead.status != http.StatusUnauthorized {
		t.Fatalf("GET %s without a session = %d %s, want the unchanged session gate", readPaths[1], anonymousRead.status, anonymousRead.body)
	}
	if code := harness.String(t, failure(t, anonymousRead), "code"); code != controlplane.CodeUnauthenticated {
		t.Fatalf("the session gate code = %q, want %q", code, controlplane.CodeUnauthenticated)
	}
}

// assertReadShape checks the single device page still reads as the read-only
// page it declares, so the refusal did not change the success shape.
func assertReadShape(t *testing.T, page map[string]any) {
	t.Helper()
	if !harness.Bool(t, page, "readOnly") {
		t.Fatalf("the device page does not state it is read-only: %+v", page)
	}
	if reason := harness.String(t, page, "readOnlyReason"); reason != devicemanager.ReadOnlyReason {
		t.Fatalf("the device page reason = %q, want %q", reason, devicemanager.ReadOnlyReason)
	}
	if items := harness.Entries(t, page, "items"); len(items) == 0 {
		t.Fatal("the device page carries no item")
	}
}

// assertRefusal checks one write answer against KOSD-C-89 and KOSD-C-12: the
// method-not-allowed transport state, the allowed methods, the error envelope,
// the page read-only code distinct from the cluster read-only code, and the
// message naming the reason.
func assertRefusal(t *testing.T, caller, method, path string, answer response) {
	t.Helper()
	if answer.status != http.StatusMethodNotAllowed {
		t.Fatalf("%s %s %s = %d %s", caller, method, path, answer.status, answer.body)
	}
	if allow := answer.header.Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("%s %s %s Allow = %q, want GET, HEAD", caller, method, path, allow)
	}
	if contentType := answer.header.Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("%s %s %s Content-Type = %q, want application/json", caller, method, path, contentType)
	}
	carried := failure(t, answer)
	code := harness.String(t, carried, "code")
	if code != controlplane.CodePageReadOnly {
		t.Fatalf("%s %s %s code = %q, want %q", caller, method, path, code, controlplane.CodePageReadOnly)
	}
	if code == controlplane.CodeReadOnly {
		t.Fatalf("%s %s %s code = %q, want a code distinct from the degraded cluster read-only code", caller, method, path, code)
	}
	if stage := harness.String(t, carried, "stage"); stage != string(controlplane.StageValidate) {
		t.Fatalf("%s %s %s stage = %q, want %q", caller, method, path, stage, controlplane.StageValidate)
	}
	if retryable := harness.Bool(t, carried, "retryable"); retryable {
		t.Fatalf("%s %s %s is marked retryable", caller, method, path)
	}
	if requiresUserAction := harness.Bool(t, carried, "requiresUserAction"); !requiresUserAction {
		t.Fatalf("%s %s %s does not ask for a user action", caller, method, path)
	}
	if sideEffects := harness.String(t, carried, "sideEffects"); sideEffects != string(controlplane.SideEffectsNone) {
		t.Fatalf("%s %s %s sideEffects = %q, want %q", caller, method, path, sideEffects, controlplane.SideEffectsNone)
	}

	message := harness.String(t, carried, "message")
	if path == "/api/v1/pages" {
		if !strings.Contains(message, "页面读取面只读") {
			t.Fatalf("%s %s %s message = %q, want the page-read surface wording", caller, method, path, message)
		}
		return
	}
	if !strings.Contains(message, devicemanager.ReadOnlyReason) {
		t.Fatalf("%s %s %s message = %q, want the declared read-only reason %q", caller, method, path, message, devicemanager.ReadOnlyReason)
	}
}
