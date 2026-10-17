package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikaelstaldal/go-server-common/csrf"
	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/internal/token"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const rawMessage = "From: Alice <alice@example.com>\r\n" +
	"To: Bob <bob@example.com>\r\n" +
	"Subject: Quarterly budget\r\n" +
	"Message-ID: <budget@example.com>\r\n" +
	"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
	"\r\n" +
	"The budget review is on Friday.\r\n"

// fixture is a real MyMail API behind the real bearer middleware and token
// store — authorization is the server's, so the tests must not mock it.
type fixture struct {
	url        string
	db         *sql.DB
	store      *token.Store
	secret     string
	message    int64 // in the token's folder
	other      int64 // in a folder the token was not granted
	draft      int64 // in the token's folder, with no raw message
	empty      int64 // in the token's folder, with no plain-text body
	attachment int64 // on message
	// on other
	otherAttachment int64
	requests        *recorder
}

// recorder keeps the method and path of every request the fixture answers,
// so the drift test sees what the MCP server actually requests rather than
// what it would resolve a URI to.
type recorder struct {
	mu       sync.Mutex
	requests []string
}

func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests := r.requests
	r.requests = nil
	return requests
}

func insertMessage(t *testing.T, db *sql.DB, folder int, subject, text string, raw []byte) int64 {
	t.Helper()
	id, err := repository.NewMessageRepository(db).InsertMessage(context.Background(), model.DBMessage{
		FolderID: folder,
		FromAddr: "Alice <alice@example.com>",
		ToAddr:   "Bob <bob@example.com>",
		Subject:  subject,
		Date:     time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC),
		BodyText: text,
		BodyHTML: "<p>" + text + "</p>",
		Raw:      raw,
	})
	require.NoError(t, err)
	return id
}

// newFixture serves the API. With basicAuth, a request without a bearer token
// gets 401, as from a server with Basic authentication configured; without
// it, such a request reaches the API, as from a server with none.
func newFixture(t *testing.T, basicAuth bool) *fixture {
	t.Helper()
	db, err := repository.OpenDB(filepath.Join(t.TempDir(), "mymail.sqlite"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0),(2,'Private','private',1)`)
	require.NoError(t, err)
	f := &fixture{db: db, requests: &recorder{}}
	f.message = insertMessage(t, db, 1, "Quarterly budget", "The budget review is on Friday.", []byte(rawMessage))
	f.other = insertMessage(t, db, 2, "Private budget", "Not for the token.", []byte(rawMessage))
	f.draft = insertMessage(t, db, 1, "Draft budget", "Unsent.", nil)
	f.empty = insertMessage(t, db, 1, "HTML only", "", []byte(rawMessage))
	f.attachment, err = repository.NewAttachmentRepository(db).InsertAttachment(context.Background(), model.DBAttachment{
		MessageID: int(f.message), Filename: "budget.pdf", ContentType: "application/pdf", Size: 9, Data: []byte("%PDF-1.4\n"),
	})
	require.NoError(t, err)
	f.otherAttachment, err = repository.NewAttachmentRepository(db).InsertAttachment(context.Background(), model.DBAttachment{
		MessageID: int(f.other), Filename: "private.pdf", ContentType: "application/pdf", Size: 9, Data: []byte("%PDF-1.4\n"),
	})
	require.NoError(t, err)
	f.store = &token.Store{DB: db}
	_, f.secret, err = f.store.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)

	h := handler.New(
		repository.NewFolderRepository(db), repository.NewMessageRepository(db), repository.NewAttachmentRepository(db),
		repository.NewDraftRepository(db), repository.NewContactRepository(db), repository.NewIdentityRepository(db),
		repository.NewFilterRepository(db), repository.NewSpamFilterRepository(db), "",
	)
	generated, err := api.NewServer(h, api.WithPathPrefix("/api/v1"), api.WithErrorHandler(handler.WriteError))
	require.NoError(t, err)
	mux := http.NewServeMux()
	token.MountDeviceGrant(mux, f.store.DeviceGrant("http://127.0.0.1/device-approval.html"))
	mux.HandleFunc("/api/v1/tokens", f.store.Management)
	mux.HandleFunc("/api/v1/tokens/", f.store.Management)
	mux.Handle("/api/v1/", generated)
	// As main's selectAuthentication: bearer requests take the restricted
	// path, everything else the full-access one, behind Basic authentication
	// when it is configured, with the device-login routes open regardless.
	common := csrf.MiddlewareOrigins("http://example.test")
	bearer := common(f.store.Bearer(mux))
	full := common(mux)
	if basicAuth {
		full = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	}
	full = token.PublicDeviceAccess(full, common(mux))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.mu.Lock()
		f.requests.requests = append(f.requests.requests, r.Method+" "+r.URL.EscapedPath())
		f.requests.mu.Unlock()
		scheme, _, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if strings.EqualFold(scheme, "Bearer") {
			bearer.ServeHTTP(w, r)
		} else {
			full.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.url = server.URL
	return f
}

func (f *fixture) tokenFile(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte(f.secret+"\n"), 0o600))
	return path
}

func (f *fixture) isRead(t *testing.T, id int64) bool {
	var read bool
	require.NoError(t, f.db.QueryRow(`SELECT read FROM messages WHERE id=?`, id).Scan(&read))
	return read
}

// connect configures a client from flags, exactly as the binary does, and
// connects an MCP session to it in memory.
func connect(t *testing.T, args ...string) *mcp.ClientSession {
	t.Helper()
	t.Setenv("MYMAIL_URL", "")
	t.Setenv("MYMAIL_TOKEN_FILE", "")
	c, help, err := configure(args)
	require.NoError(t, err)
	require.False(t, help)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := newServer(c).Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func read(t *testing.T, session *mcp.ClientSession, uri string) (*mcp.ResourceContents, error) {
	t.Helper()
	result, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	require.Len(t, result.Contents, 1)
	return result.Contents[0], nil
}

func call(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	return result
}

func callText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text
}

func id(n int64) string { return strconv.FormatInt(n, 10) }

// TestSurfaceMatchesTokenSurface is the drift test (mcp-server.md §4.1):
// every resource, template and tool requests only API operations a token may
// use, and every such operation is requested by one of them or excluded.
//
// Its bound: the operations a token may use are enumerated from openapi.yaml,
// probed through the real bearer middleware with IDs the token can reach. An
// operation openapi.yaml does not document is invisible to it.
func TestSurfaceMatchesTokenSurface(t *testing.T) {
	f := newFixture(t, true)
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	var document struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &document))
	parameter := regexp.MustCompile(`\\\{[a-z_0-9]+\\\}`)
	type operation struct {
		method  string
		pattern *regexp.Regexp
	}
	operations := map[string]operation{}
	permitted := []string{}
	for path, methods := range document.Paths {
		concrete := strings.NewReplacer("{folder_id}", "1", "{attachment_id}", id(f.attachment), "{user_code}", "ABCD-EFGH", "{slug}", "x").Replace(path)
		if strings.HasPrefix(path, "/attachments/") {
			concrete = strings.ReplaceAll(concrete, "{id}", id(f.attachment))
		} else {
			concrete = strings.ReplaceAll(concrete, "{id}", id(f.message))
		}
		for method := range methods {
			method = strings.ToUpper(method)
			if !slices.Contains([]string{"GET", "PUT", "POST", "PATCH", "DELETE"}, method) {
				continue
			}
			key := method + " " + path
			operations[key] = operation{method, regexp.MustCompile("^/api/v1" + parameter.ReplaceAllString(regexp.QuoteMeta(path), "[^/]+") + "$")}
			request, err := http.NewRequest(method, f.url+"/api/v1"+concrete, nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+f.secret)
			if path == "/messages/search" {
				request.URL.RawQuery = "folder_id=1&q=budget"
			}
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				permitted = append(permitted, key)
			}
		}
	}
	require.NotEmpty(t, permitted)

	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	ctx := context.Background()
	samples := map[string]string{
		"mymail://folders":                       "mymail://folders",
		messagesTemplate:                         "mymail://folders/1/messages?limit=5",
		"mymail://messages/{id}":                 "mymail://messages/" + id(f.message),
		"mymail://messages/{id}/text":            "mymail://messages/" + id(f.message) + "/text",
		"mymail://messages/{id}/headers":         "mymail://messages/" + id(f.message) + "/headers",
		"mymail://messages/{id}/body{?external}": "mymail://messages/" + id(f.message) + "/body",
		"mymail://messages/{id}/raw":             "mymail://messages/" + id(f.message) + "/raw",
		"mymail://attachments/{id}":              "mymail://attachments/" + id(f.attachment),
	}
	// Each tool's operation is pinned: a resource covering the same operation
	// would otherwise hide a tool that requests the wrong one.
	toolSamples := map[string]struct {
		arguments map[string]any
		operation string
	}{
		"search_messages":   {map[string]any{"folder_id": 1, "q": "budget"}, "GET /messages/search"},
		"mark_message_read": {map[string]any{"id": f.message}, "PUT /messages/{id}/read"},
	}
	covered := []string{}
	// cover records the API operations one read or call requested.
	cover := func(what string) []string {
		requested := f.requests.take()
		require.NotEmpty(t, requested, "%s made no request", what)
		documented := []string{}
		for _, request := range requested {
			method, path, _ := strings.Cut(request, " ")
			matched := ""
			// A literal path wins over a parameterised one: /messages/search
			// also matches /messages/{id}.
			for key, op := range operations {
				if op.method == method && op.pattern.MatchString(path) &&
					(matched == "" || strings.Count(key, "{") < strings.Count(matched, "{")) {
					matched = key
				}
			}
			require.NotEmpty(t, matched, "%s requested %s, which is not in openapi.yaml", what, request)
			covered = append(covered, matched)
			documented = append(documented, matched)
		}
		return documented
	}
	f.requests.take()
	listed := []string{}
	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	for _, resource := range resources.Resources {
		listed = append(listed, resource.URI)
	}
	templates, err := session.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)
	for _, template := range templates.ResourceTemplates {
		listed = append(listed, template.URITemplate)
	}
	for _, uri := range listed {
		sample, ok := samples[uri]
		require.True(t, ok, "no sample for %s", uri)
		_, err := read(t, session, sample)
		require.NoError(t, err, sample)
		cover(uri)
	}
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range tools.Tools {
		sample, ok := toolSamples[tool.Name]
		require.True(t, ok, "no sample for tool %s", tool.Name)
		result := call(t, session, tool.Name, sample.arguments)
		require.False(t, result.IsError, tool.Name)
		assert.Equal(t, []string{sample.operation}, cover(tool.Name), tool.Name)
	}

	// mcp-server.md §2.3 excludes nothing from MyMail's API but HEAD, which is
	// not probed here.
	slices.Sort(covered)
	covered = slices.Compact(covered)
	slices.Sort(permitted)
	assert.Equal(t, permitted, covered)
}

func TestResourcesReturnWhatTheAPIReturns(t *testing.T) {
	f := newFixture(t, true)
	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	for _, tc := range []struct {
		uri, mime, contains string
	}{
		{"mymail://folders", "application/json", `"name":"Inbox"`},
		{"mymail://folders/1/messages", "application/json", "Quarterly budget"},
		{"mymail://folders/1/messages?flagged=false&limit=10&offset=0", "application/json", "Quarterly budget"},
		{"mymail://messages/" + id(f.message), "application/json", `"body_text":"The budget review is on Friday."`},
		{"mymail://messages/" + id(f.message), "application/json", `"warnings":[{"kind":"first_time_sender"}]`},
		{"mymail://messages/" + id(f.message) + "/text", "text/plain", "The budget review is on Friday."},
		{"mymail://messages/" + id(f.message) + "/headers", "text/plain", "Subject: Quarterly budget"},
		{"mymail://messages/" + id(f.message) + "/body?external=1", "text/html", "The budget review is on Friday."},
	} {
		contents, err := read(t, session, tc.uri)
		require.NoError(t, err, tc.uri)
		assert.Equal(t, tc.uri, contents.URI)
		assert.Equal(t, tc.mime, contents.MIMEType, tc.uri)
		assert.Contains(t, contents.Text, tc.contains, tc.uri)
		assert.Empty(t, contents.Blob, tc.uri)
	}
	contents, err := read(t, session, "mymail://folders")
	require.NoError(t, err)
	assert.NotContains(t, contents.Text, "Private", "the server filters folders to the token's")

	contents, err = read(t, session, "mymail://messages/"+id(f.message)+"/text")
	require.NoError(t, err)
	assert.Equal(t, "The budget review is on Friday.", contents.Text, "only the plain-text body")

	contents, err = read(t, session, "mymail://messages/"+id(f.message)+"/raw")
	require.NoError(t, err)
	assert.Equal(t, "message/rfc822", contents.MIMEType)
	assert.Equal(t, []byte(rawMessage), contents.Blob)

	contents, err = read(t, session, "mymail://attachments/"+id(f.attachment))
	require.NoError(t, err)
	assert.Equal(t, []byte("%PDF-1.4\n"), contents.Blob)
	assert.Empty(t, contents.Text)

	// A message with no plain-text body: an empty blob, not contents that are
	// neither text nor blob (mcp-server.md §2.4).
	contents, err = read(t, session, "mymail://messages/"+id(f.empty)+"/text")
	require.NoError(t, err)
	assert.Equal(t, "text/plain", contents.MIMEType)
	assert.NotNil(t, contents.Blob)
	assert.Empty(t, contents.Blob)

	_, err = read(t, session, "mymail://messages/"+id(f.draft)+"/raw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a draft")
}

func TestScope(t *testing.T) {
	f := newFixture(t, true)
	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	for _, uri := range []string{
		"mymail://folders/2/messages",
		"mymail://messages/" + id(f.other),
		"mymail://messages/" + id(f.other) + "/text",
		"mymail://messages/" + id(f.other) + "/headers",
		"mymail://messages/" + id(f.other) + "/body",
		"mymail://messages/" + id(f.other) + "/raw",
		"mymail://attachments/" + id(f.otherAttachment),
	} {
		_, err := read(t, session, uri)
		require.Error(t, err, uri)
		assert.Contains(t, err.Error(), "outside this token's scope", uri)
	}
	result := call(t, session, "search_messages", map[string]any{"folder_id": 2, "q": "budget"})
	assert.True(t, result.IsError)
	assert.Contains(t, callText(t, result), "outside this token's scope")

	result = call(t, session, "mark_message_read", map[string]any{"id": f.other})
	assert.True(t, result.IsError)
	assert.Contains(t, callText(t, result), "outside this token's scope")
	assert.False(t, f.isRead(t, f.other))
}

func TestSearchMessagesTool(t *testing.T) {
	f := newFixture(t, true)
	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var search *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "search_messages" {
			search = tool
		}
	}
	require.NotNil(t, search)
	require.NotNil(t, search.Annotations)
	assert.True(t, search.Annotations.ReadOnlyHint)
	require.NotNil(t, search.Annotations.OpenWorldHint)
	assert.False(t, *search.Annotations.OpenWorldHint)
	schema, err := json.Marshal(search.InputSchema)
	require.NoError(t, err)
	var parsed struct {
		Required []string `json:"required"`
	}
	require.NoError(t, json.Unmarshal(schema, &parsed))
	slices.Sort(parsed.Required)
	assert.Equal(t, []string{"folder_id", "q"}, parsed.Required, "the server requires a folder for bearer searches")

	result := call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "budget", "sort": "date_desc", "limit": 10})
	assert.False(t, result.IsError)
	assert.Contains(t, callText(t, result), "Quarterly budget")
	assert.NotContains(t, callText(t, result), "Private budget")
	assert.NotNil(t, result.StructuredContent)

	result = call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "budget", "from_addr": "nobody"})
	assert.False(t, result.IsError)
	assert.NotContains(t, callText(t, result), "Quarterly budget", "the address filter is forwarded")

	result = call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "budget", "sort": "sideways"})
	assert.True(t, result.IsError)
	assert.Contains(t, callText(t, result), "HTTP 400: ")
}

func TestMarkMessageRead(t *testing.T) {
	f := newFixture(t, true)
	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var mark *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "mark_message_read" {
			mark = tool
		}
	}
	require.NotNil(t, mark)
	require.NotNil(t, mark.Annotations)
	assert.False(t, mark.Annotations.ReadOnlyHint)
	require.NotNil(t, mark.Annotations.DestructiveHint)
	assert.False(t, *mark.Annotations.DestructiveHint)
	assert.True(t, mark.Annotations.IdempotentHint)
	require.NotNil(t, mark.Annotations.OpenWorldHint)
	assert.False(t, *mark.Annotations.OpenWorldHint)

	require.False(t, f.isRead(t, f.message))
	for range 2 {
		result := call(t, session, "mark_message_read", map[string]any{"id": f.message})
		assert.False(t, result.IsError)
		assert.Contains(t, callText(t, result), "marked as read")
		assert.True(t, f.isRead(t, f.message))
	}
	result := call(t, session, "mark_message_read", map[string]any{"id": 0})
	assert.True(t, result.IsError)
}

func TestArgumentsAreValidated(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	session := connect(t, "-url", server.URL)
	for _, tc := range []struct {
		name      string
		arguments map[string]any
	}{
		{"search_messages", map[string]any{"q": "x"}},
		{"search_messages", map[string]any{"folder_id": 1}},
		{"search_messages", map[string]any{"folder_id": 1, "q": "x", "unread": true}},
		{"mark_message_read", map[string]any{}},
		{"mark_message_read", map[string]any{"id": "1"}},
	} {
		result := call(t, session, tc.name, tc.arguments)
		assert.True(t, result.IsError, "%s %v", tc.name, tc.arguments)
	}
	assert.Zero(t, requests.Load(), "a refused call makes no HTTP request")
}

func TestQueryParametersAreParsedHere(t *testing.T) {
	var requests atomic.Int32
	var last atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		last.Store(r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	session := connect(t, "-url", server.URL)

	_, err := read(t, session, "mymail://folders/1/messages?unread=true&offset=5&limit=2")
	require.NoError(t, err)
	assert.Equal(t, "limit=2&offset=5&unread=true", last.Load())

	before := requests.Load()
	for uri, want := range map[string]struct {
		message string
		code    int64
	}{
		// Refused here with the internal-error code (mcp-server.md §5.8).
		"mymail://folders/1/messages?q=text":          {"use the search_messages tool", jsonrpc.CodeInternalError},
		"mymail://folders/1/messages?limit=1&limit=2": {"only once", jsonrpc.CodeInternalError},
		"mymail://messages/abc":                       {"positive integer", jsonrpc.CodeInternalError},
		"mymail://messages/01":                        {"positive integer", jsonrpc.CodeInternalError},
		// No template takes a query here, so the SDK's dispatch refuses it.
		"mymail://folders?limit=1":   {"Resource not found", jsonrpc.CodeInvalidParams},
		"mymail://messages/1?x=1":    {"Resource not found", jsonrpc.CodeInvalidParams},
		"mymail://messages/1/thread": {"Resource not found", jsonrpc.CodeInvalidParams},
	} {
		_, err := read(t, session, uri)
		require.Error(t, err, uri)
		assert.Contains(t, err.Error(), want.message, uri)
		var wire *jsonrpc.Error
		require.ErrorAs(t, err, &wire, uri)
		assert.Equal(t, want.code, wire.Code, uri)
	}
	assert.Equal(t, before, requests.Load(), "a refused URI makes no HTTP request")
}

func TestResponseCap(t *testing.T) {
	size := maxBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/attachments/"):
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(bytes.Repeat([]byte{0x89}, size))
		case strings.HasSuffix(r.URL.Path, "/headers"):
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(bytes.Repeat([]byte("h"), size))
		default:
			w.Header().Set("Content-Type", "application/json")
			body := `{"items":[],"pad":"` + strings.Repeat("p", size-len(`{"items":[],"pad":""}`)) + `"}`
			_, _ = w.Write([]byte(body))
		}
	}))
	defer server.Close()
	session := connect(t, "-url", server.URL)
	search := func() *mcp.CallToolResult {
		return call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "x"})
	}

	contents, err := read(t, session, "mymail://messages/1/headers")
	require.NoError(t, err)
	assert.Len(t, contents.Text, maxBody)
	contents, err = read(t, session, "mymail://attachments/1")
	require.NoError(t, err)
	assert.Len(t, contents.Blob, maxBody)
	assert.False(t, search().IsError)

	size = maxBody + 1
	for _, uri := range []string{"mymail://messages/1/headers", "mymail://attachments/1"} {
		_, err := read(t, session, uri)
		require.Error(t, err, uri)
		assert.Contains(t, err.Error(), "1 MiB", uri)
	}
	result := search()
	assert.True(t, result.IsError)
	assert.Contains(t, callText(t, result), "1 MiB")
}

func TestAuthenticationErrors(t *testing.T) {
	f := newFixture(t, true)

	session := connect(t, "-url", f.url)
	_, err := read(t, session, "mymail://folders")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires authentication; set MYMAIL_TOKEN_FILE")

	tokenFile := f.tokenFile(t)
	records, err := f.store.List()
	require.NoError(t, err)
	_, err = f.store.Revoke(records[0].Slug)
	require.NoError(t, err)
	session = connect(t, "-url", f.url, "-token-file", tokenFile)
	_, err = read(t, session, "mymail://folders")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired or revoked; run mymail-token login")
	assert.NotContains(t, err.Error(), f.secret)
	result := call(t, session, "mark_message_read", map[string]any{"id": f.message})
	assert.True(t, result.IsError)
	assert.Contains(t, callText(t, result), "expired or revoked")
	assert.NotContains(t, callText(t, result), f.secret)
	assert.False(t, f.isRead(t, f.message))
}

func TestUnauthenticatedAgainstOpenServer(t *testing.T) {
	f := newFixture(t, false)
	session := connect(t, "-url", f.url)
	contents, err := read(t, session, "mymail://messages/"+id(f.other)+"/text")
	require.NoError(t, err)
	assert.Equal(t, "Not for the token.", contents.Text)
	result := call(t, session, "mark_message_read", map[string]any{"id": f.other})
	assert.False(t, result.IsError)
	assert.True(t, f.isRead(t, f.other))
	_, err = read(t, session, "mymail://messages/999999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Resource not found")
}

func TestServerDown(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	session := connect(t, "-url", server.URL)
	_, err := read(t, session, "mymail://folders")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
	assert.True(t, call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "x"}).IsError)
	assert.True(t, call(t, session, "mark_message_read", map[string]any{"id": 1}).IsError)
}

func TestServerErrors(t *testing.T) {
	const secret = "review-test-secret"
	longError := strings.Repeat("e", 5000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/folders":
			// A server or proxy echoing the credential back.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"` + r.Header.Get("Authorization") + `"}`))
		case "/api/v1/messages/1/headers":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream saw " + r.Header.Get("Authorization")))
		case "/api/v1/messages/2/headers":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"` + longError + `"}`))
		case "/api/v1/messages/3/headers":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"` + strings.Repeat("h", maxBody) + `"}`))
		case "/api/v1/messages/4/headers":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error_message":"decode params"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"failed for ` + r.Header.Get("Authorization") + `"}`))
		}
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(secret), 0o600))
	session := connect(t, "-url", server.URL, "-token-file", tokenFile)

	for uri, want := range map[string]string{
		"mymail://folders":            "HTTP 500: Bearer [token]",
		"mymail://messages/1/headers": "HTTP 502",
		"mymail://messages/2/headers": "HTTP 500: " + longError,
		"mymail://messages/4/headers": "HTTP 400: decode params",
	} {
		_, err := read(t, session, uri)
		require.Error(t, err, uri)
		assert.Contains(t, err.Error(), want, uri)
		assert.NotContains(t, err.Error(), secret, uri)
	}
	_, err := read(t, session, "mymail://messages/3/headers")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hhhh", "an error body over the cap is not shown, not even in part")

	for name, arguments := range map[string]map[string]any{
		"search_messages":   {"folder_id": 1, "q": "x"},
		"mark_message_read": {"id": 1},
	} {
		result := call(t, session, name, arguments)
		assert.True(t, result.IsError, name)
		assert.Contains(t, callText(t, result), "failed for Bearer [token]", name)
		assert.NotContains(t, callText(t, result), secret, name)
	}
}

func TestErrorField(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":"primary"}`:                             "primary",
		`{"error_message":"secondary"}`:                   "secondary",
		`{"error":"primary","error_message":"secondary"}`: "primary",
		`{"error":"primary","error_message":123}`:         "primary",
		`{"error":123,"error_message":"secondary"}`:       "secondary",
		`{"message":"neither"}`:                           "",
		`not json`:                                        "",
	} {
		assert.Equal(t, want, errorField([]byte(body)), body)
	}
}

func TestSearchJSONIsPassedThrough(t *testing.T) {
	const body = " { \"items\" : [], \"total\" : 0 }\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	session := connect(t, "-url", server.URL)
	result := call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "x"})
	assert.Equal(t, body, callText(t, result))
	assert.Equal(t, map[string]any{"items": []any{}, "total": float64(0)}, result.StructuredContent)
}

func TestCompletion(t *testing.T) {
	f := newFixture(t, true)
	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	complete := func(uri, argument, value string) []string {
		result, err := session.Complete(context.Background(), &mcp.CompleteParams{
			Ref:      &mcp.CompleteReference{Type: "ref/resource", URI: uri},
			Argument: mcp.CompleteParamsArgument{Name: argument, Value: value},
		})
		require.NoError(t, err)
		return result.Completion.Values
	}
	assert.Equal(t, []string{"1"}, complete(messagesTemplate, "folder_id", ""), "only the token's folders")
	assert.Empty(t, complete(messagesTemplate, "folder_id", "2"))
	assert.Empty(t, complete(messagesTemplate, "limit", ""))
	assert.Empty(t, complete("mymail://messages/{id}", "id", ""))
}

func TestCapabilities(t *testing.T) {
	session := connect(t)
	initialized := session.InitializeResult()
	assert.Equal(t, "mymail-mcp", initialized.ServerInfo.Name)
	capabilities := initialized.Capabilities
	require.NotNil(t, capabilities.Resources)
	assert.False(t, capabilities.Resources.Subscribe)
	assert.False(t, capabilities.Resources.ListChanged)
	require.NotNil(t, capabilities.Tools)
	assert.False(t, capabilities.Tools.ListChanged)
	assert.NotNil(t, capabilities.Completions)
	assert.Nil(t, capabilities.Prompts)
	// Logging is deprecated in the SDK's Go API, so check the wire form.
	wire, err := json.Marshal(capabilities)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), `"logging"`)
}

func TestStartup(t *testing.T) {
	t.Setenv("MYMAIL_URL", "")
	t.Setenv("MYMAIL_TOKEN_FILE", "")
	var stdout bytes.Buffer
	require.NoError(t, run(context.Background(), []string{"-help"}, &stdout, nil))
	assert.Contains(t, stdout.String(), "Usage: mymail-mcp")

	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	for _, args := range [][]string{
		{"-url", "http://example.com"},
		{"-url", "not a url"},
		{"-token-file", filepath.Join(t.TempDir(), "missing")},
		{"-token-file", empty},
		{"-token-file", ""},
		{"extra"},
	} {
		stdout.Reset()
		err := run(context.Background(), args, &stdout, nil)
		assert.Error(t, err, fmt.Sprint(args))
		assert.Empty(t, stdout.String(), fmt.Sprint(args))
	}

	t.Setenv("MYMAIL_TOKEN_FILE", empty)
	_, _, err := configure(nil)
	assert.Error(t, err, "the environment's token file is read too")
}

func TestSuccessfulResponseEchoingTheTokenIsRefused(t *testing.T) {
	const secret = "review-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/messages/2" {
			// The token JSON-escaped, which decodes to the secret.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"body_text":"\u0072eview-test-secret","items":[{"id":1}]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/attachments/") {
			w.Header().Set("Content-Type", "application/octet-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = w.Write([]byte(`{"echo":"` + r.Header.Get("Authorization") + `"}`))
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(secret), 0o600))
	session := connect(t, "-url", server.URL, "-token-file", tokenFile)
	for _, uri := range []string{"mymail://folders", "mymail://messages/1/text", "mymail://attachments/1", "mymail://messages/2/text"} {
		_, err := read(t, session, uri)
		require.Error(t, err, uri)
		assert.Contains(t, err.Error(), "contains the API token", uri)
		assert.NotContains(t, err.Error(), secret, uri)
	}
	result := call(t, session, "search_messages", map[string]any{"folder_id": 1, "q": "x"})
	assert.True(t, result.IsError)
	assert.NotContains(t, callText(t, result), secret)
	assert.Nil(t, result.StructuredContent)
}

// TestTokenHiddenInJSON: a token JSON-escaped in an earlier duplicate member,
// which a decoded map would drop, and in an error body shown as it is.
func TestTokenHiddenInJSON(t *testing.T) {
	const secret = "review-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/messages/7":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"x":"\u0072eview-test-secret","x":"safe"}`))
		case "/api/v1/messages/8":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"Bearer \u0072eview-test-secret"}`))
		}
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(secret), 0o600))
	session := connect(t, "-url", server.URL, "-token-file", tokenFile)
	_, err := read(t, session, "mymail://messages/7")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains the API token")
	_, err = read(t, session, "mymail://messages/8")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 502")
	assert.NotContains(t, err.Error(), "eview-test-secret", "the body is not shown")
}

func TestScopedWarningsIntentionallyUseGlobalCorrespondence(t *testing.T) {
	f := newFixture(t, true)
	// The token grants Inbox alone; Sent/private messages remain inaccessible.
	_, err := repository.NewMessageRepository(f.db).InsertMessage(context.Background(), model.DBMessage{FolderID: 2, ToAddr: "alice@example.com"})
	require.NoError(t, err)
	session := connect(t, "-url", f.url, "-token-file", f.tokenFile(t))
	contents, err := read(t, session, "mymail://messages/"+id(f.message))
	require.NoError(t, err)
	assert.Contains(t, contents.Text, `"warnings":[]`, "global sent-to history is intentional derived metadata")
	_, err = read(t, session, "mymail://messages/"+id(f.other))
	require.Error(t, err, "does not expose underlying ungranted mail")
}
