package chart

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// chartCall is one Versions or VersionsAuth read.
// repoKind is https, oci, or local. A local read writes Chart.yaml from
// chartYAML and passes localRepo through; the directory wins over that URL.
// {server}, {repo}, {indexURL}, and {basic} in expected errors are filled
// once the server exists. {basic} is "Basic " plus base64(username:password).
type chartCall struct {
	Fn        string `json:"fn"`
	RepoKind  string `json:"repoKind"`
	RepoPath  string `json:"repoPath,omitempty"`
	Chart     string `json:"chart"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	LocalRepo string `json:"localRepo,omitempty"`
	ChartYAML string `json:"chartYAML,omitempty"`

	Versions         *[]string `json:"versions,omitempty"`
	ErrorExact       string    `json:"errorExact,omitempty"`
	ErrorContains    []string  `json:"errorContains,omitempty"`
	ErrorNotContains []string  `json:"errorNotContains,omitempty"`
	NeedsLogin       bool      `json:"needsLogin,omitempty"`
	CapturedAuth     string    `json:"capturedAuth,omitempty"`
	CapturedEquals   string    `json:"capturedEquals,omitempty"`
}

// chartRoute is one httptest response. The first matching route wins.
// A route with no path matches every path still unmatched.
// No match answers 404 with body "404 page not found\n".
// query selects the route. requireQuery on a selected route answers 400
// "bad scope ..." when the query does not match, as the token endpoint does.
// requireAuth skips the route when Authorization differs.
// captureAuth records Authorization when the path and query match.
type chartRoute struct {
	Path         string            `json:"path"`
	Query        map[string]string `json:"query,omitempty"`
	Status       int               `json:"status,omitempty"`
	Body         string            `json:"body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Challenge    string            `json:"challenge,omitempty"`
	RealmPath    string            `json:"realmPath,omitempty"`
	RealmName    string            `json:"realmName,omitempty"`
	Service      string            `json:"service,omitempty"`
	Scope        string            `json:"scope,omitempty"`
	RequireAuth  string            `json:"requireAuth,omitempty"`
	RequireQuery map[string]string `json:"requireQuery,omitempty"`
	CaptureAuth  string            `json:"captureAuth,omitempty"`
}

type repoCase struct {
	Name          string       `json:"name,omitempty"`
	Description   string       `json:"description,omitempty"`
	TLS           bool         `json:"tls,omitempty"`
	ReplaceClient bool         `json:"replaceClient,omitempty"`
	Username      string       `json:"username,omitempty"`
	Password      string       `json:"password,omitempty"`
	Routes        []chartRoute `json:"routes,omitempty"`
	Calls         []chartCall  `json:"calls"`
}

func TestVersionsFromARepoIndex(t *testing.T) {
	cases.Run(t, "testdata/repo/versions-from-a-repo-index", runRepoCase)
}

func TestVersionsFromAnOCIRegistry(t *testing.T) {
	cases.Run(t, "testdata/repo/versions-from-an-oci-registry", runRepoCase)
}

func TestVersionsFromALocalChart(t *testing.T) {
	cases.Run(t, "testdata/repo/versions-from-a-local-chart", runRepoCase)
}

func TestARefusedReadSaysItNeedsALogin(t *testing.T) {
	cases.Run(t, "testdata/repo/a-refused-read-says-it-needs-a-login", runRepoCase)
}

func TestARegistryAskingForBasicAuthNeedsALogin(t *testing.T) {
	cases.Run(t, "testdata/repo/a-registry-asking-for-basic-auth-needs-a-login", runRepoCase)
}

func TestVersionsAuthSendsBasicAuth(t *testing.T) {
	cases.Run(t, "testdata/repo/versions-auth-sends-basic-auth", runRepoCase)
}

func TestAMissingIndexIsReportedByStatus(t *testing.T) {
	cases.Run(t, "testdata/repo/a-missing-index-is-reported-by-status", runRepoCase)
}

func runRepoCase(t *testing.T, tc repoCase) {
	t.Helper()
	if len(tc.Calls) == 0 {
		t.Fatal("no calls")
	}
	if tc.ReplaceClient && !tc.TLS {
		t.Fatal("replaceClient is only for a TLS server")
	}
	basic := ""
	if tc.Username != "" || tc.Password != "" {
		basic = basicHeader(tc.Username, tc.Password)
	}
	needsServer := false
	for _, call := range tc.Calls {
		if call.RepoKind == "https" || call.RepoKind == "oci" {
			needsServer = true
		}
	}
	var srv *httptest.Server
	serverURL := ""
	captured := map[string]string{}
	saved := client
	if needsServer {
		handler := repoHandler(&serverURL, tc.Routes, captured, basic)
		if tc.TLS {
			srv = httptest.NewTLSServer(handler)
		} else {
			srv = httptest.NewServer(handler)
		}
		serverURL = srv.URL
		t.Cleanup(srv.Close)
		if tc.ReplaceClient {
			client = srv.Client()
			t.Cleanup(func() { client = saved })
		}
	}
	for _, call := range tc.Calls {
		runChartCall(t, srv, serverURL, basic, captured, call)
	}
	if !tc.ReplaceClient && client != saved {
		t.Errorf("package client changed")
	}
}

func runChartCall(t *testing.T, srv *httptest.Server, serverURL, basic string, captured map[string]string, call chartCall) {
	t.Helper()
	repo, path := call.source(t, srv)
	indexURL := ""
	if call.RepoKind == "https" {
		indexURL = strings.TrimSuffix(repo, "/") + "/index.yaml"
	}
	callBasic := basic
	if call.Username != "" || call.Password != "" {
		callBasic = basicHeader(call.Username, call.Password)
	}
	var (
		got []string
		err error
	)
	switch call.Fn {
	case "versions":
		if call.Username != "" || call.Password != "" {
			t.Fatal("versions is anonymous; use versionsAuth")
		}
		got, err = Versions(t.Context(), repo, path, call.Chart)
	case "versionsAuth":
		got, err = VersionsAuth(t.Context(), repo, path, call.Chart, Credentials{
			Username: call.Username,
			Password: call.Password,
		})
	default:
		t.Fatalf("fn %q", call.Fn)
	}
	switch {
	case call.Versions != nil:
		wantAuth, auth := "", ""
		badAuth := false
		if call.CapturedAuth != "" {
			auth = captured[call.CapturedAuth]
			wantAuth = subst(call.CapturedEquals, serverURL, repo, indexURL, callBasic)
			badAuth = auth != wantAuth
		}
		if err != nil || !reflect.DeepEqual(got, *call.Versions) || badAuth {
			t.Fatalf("got %v, %v; want %v; auth %q want %q", got, err, *call.Versions, auth, wantAuth)
		}
	case call.NeedsLogin || call.ErrorExact != "" || len(call.ErrorContains) > 0 || len(call.ErrorNotContains) > 0:
		if call.NeedsLogin {
			var re *RepoError
			if err == nil || !errors.As(err, &re) || !re.NeedsLogin() {
				t.Fatalf("got %v, want a RepoError needing a login", err)
			}
		}
		if call.ErrorExact != "" {
			want := subst(call.ErrorExact, serverURL, repo, indexURL, callBasic)
			if err == nil || err.Error() != want {
				t.Errorf("got %v", err)
			}
		}
		checkContained(t, err, call.ErrorContains, serverURL, repo, indexURL, callBasic)
		checkAbsent(t, err, call.ErrorNotContains, serverURL, repo, indexURL, callBasic)
	default:
		t.Fatal("call has no expectation")
	}
}

// checkContained fails when err does not include every substring.
// An empty substring matches every error, so it is rejected on its own.
func checkContained(t *testing.T, err error, subs []string, serverURL, repo, indexURL, callBasic string) {
	t.Helper()
	for _, sub := range subs {
		if sub == "" {
			t.Fatal("errorContains entry is empty")
		}
		want := subst(sub, serverURL, repo, indexURL, callBasic)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

// checkAbsent fails when err includes a substring that must stay out of it.
// An empty substring matches every error, so it is rejected on its own.
func checkAbsent(t *testing.T, err error, subs []string, serverURL, repo, indexURL, callBasic string) {
	t.Helper()
	for _, sub := range subs {
		if sub == "" {
			t.Fatal("errorNotContains entry is empty")
		}
		want := subst(sub, serverURL, repo, indexURL, callBasic)
		if err != nil && strings.Contains(err.Error(), want) {
			t.Errorf("%q names the token endpoint rather than the repository", err)
		}
	}
}

func (call chartCall) source(t *testing.T, srv *httptest.Server) (repo, path string) {
	t.Helper()
	switch call.RepoKind {
	case "https":
		if srv == nil {
			t.Fatal("https repo needs a server")
		}
		return srv.URL + call.RepoPath, ""
	case "oci":
		if srv == nil {
			t.Fatal("oci repo needs a server")
		}
		return "oci://" + strings.TrimPrefix(srv.URL, "https://") + call.RepoPath, ""
	case "local":
		if call.ChartYAML == "" {
			t.Fatal("local chart needs chartYAML")
		}
		root := t.TempDir()
		dir := filepath.Join(root, call.Chart)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(call.ChartYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		return call.LocalRepo, root
	default:
		t.Fatalf("repoKind %q", call.RepoKind)
		return "", ""
	}
}

func repoHandler(serverURL *string, routes []chartRoute, captured map[string]string, basic string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := range routes {
			route := routes[i]
			if route.Path != "" && r.URL.Path != route.Path {
				continue
			}
			if !queryMatch(r, route.Query) {
				continue
			}
			if len(route.RequireQuery) > 0 && !queryMatch(r, route.RequireQuery) {
				http.Error(w, "bad scope "+r.URL.Query().Get("scope"), http.StatusBadRequest)
				return
			}
			if route.CaptureAuth != "" {
				captured[route.CaptureAuth] = r.Header.Get("Authorization")
			}
			wantAuth := subst(route.RequireAuth, *serverURL, "", "", basic)
			if route.RequireAuth != "" && r.Header.Get("Authorization") != wantAuth {
				continue
			}
			writeRoute(w, route, *serverURL)
			return
		}
		http.NotFound(w, r)
	})
}

func writeRoute(w http.ResponseWriter, route chartRoute, serverURL string) {
	if route.Challenge != "" {
		w.Header().Set("WWW-Authenticate", challengeHeader(route, serverURL))
	}
	for k, v := range route.Headers {
		w.Header().Set(k, v)
	}
	status := route.Status
	if status == 0 {
		if route.Challenge != "" {
			status = http.StatusUnauthorized
		} else {
			status = http.StatusOK
		}
	}
	w.WriteHeader(status)
	if route.Body != "" {
		_, _ = io.WriteString(w, route.Body)
	}
}

func challengeHeader(route chartRoute, serverURL string) string {
	switch route.Challenge {
	case "bearer":
		return fmt.Sprintf(`Bearer realm="%s%s",service="%s",scope="%s"`, serverURL, route.RealmPath, route.Service, route.Scope)
	case "basic":
		return fmt.Sprintf(`Basic realm="%s"`, route.RealmName)
	default:
		return route.Challenge
	}
}

func queryMatch(r *http.Request, q map[string]string) bool {
	for k, v := range q {
		if r.URL.Query().Get(k) != v {
			return false
		}
	}
	return true
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func subst(s, server, repo, indexURL, basic string) string {
	return strings.NewReplacer(
		"{server}", server,
		"{repo}", repo,
		"{indexURL}", indexURL,
		"{basic}", basic,
	).Replace(s)
}
