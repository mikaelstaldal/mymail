// Command mcp is a stdio Model Context Protocol server for the MyMail API.
//
// It is an HTTP client of a running MyMail server, like mymail-cli, and
// exposes exactly the bearer-protected API routes an API token can reach. The
// contract is ../mysuite/spec/mcp-server.md; read it before changing what is
// exposed or how.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/mikaelstaldal/mymail/internal/clihttp"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const usage = `Usage: mymail-mcp [flags]

A Model Context Protocol server on stdin/stdout, for an MCP client to start as a
subprocess. It reads the MyMail API over HTTP, exposing what an API token can reach.

Flags:
  -help             Print help and exit successfully
  -url URL          Server base URL, including optional path (default MYMAIL_URL or http://127.0.0.1:8080)
  -token-file PATH  Read API token from a file (default MYMAIL_TOKEN_FILE)

Without -token-file or MYMAIL_TOKEN_FILE, no Authorization header is sent.
HTTP is allowed only for literal loopback addresses; remote servers require HTTPS.
Redirects and environment HTTP proxies are disabled.

Resources:
  mymail://folders                        the folders the token can read
  mymail://folders/{folder_id}/messages{?limit,offset,unread,flagged}
                                          a page of a folder's messages
  mymail://messages/{id}                  one message, as JSON
  mymail://messages/{id}/text             its plain-text body
  mymail://messages/{id}/headers          its header block
  mymail://messages/{id}/body{?external}  its sanitized HTML body
  mymail://messages/{id}/raw              the original RFC 5322 message
  mymail://attachments/{id}               an attachment's bytes

Tools:
  search_messages                         full-text search in one folder
  mark_message_read                       mark a message as read
`

// maxBody is the cap on every response body read, resource or tool
// (mcp-server.md §2.4). A body over it is refused, never truncated.
const maxBody = 1 << 20

const messagesTemplate = "mymail://folders/{folder_id}/messages{?limit,offset,unread,flagged}"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "mymail-mcp:", err)
		os.Exit(1)
	}
}

// run parses the flags and serves MCP on transport until the client
// disconnects. stdout receives only -help output; the transport owns the
// process's real stdout.
func run(ctx context.Context, args []string, stdout io.Writer, transport mcp.Transport) error {
	c, help, err := configure(args)
	if help {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	if err != nil {
		return err
	}
	return newServer(c).Run(ctx, transport)
}

// configure reads the flags and the token file, failing only on bad
// configuration. An unreachable server is reported per request instead.
func configure(args []string) (*client, bool, error) {
	flags := flag.NewFlagSet("mymail-mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	base := flags.String("url", clihttp.URLDefault(), "server base URL")
	tokenFile := flags.String("token-file", os.Getenv("MYMAIL_TOKEN_FILE"), "token file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("%w\n%s", err, usage)
	}
	if flags.NArg() != 0 {
		return nil, false, fmt.Errorf("unexpected argument %q\n%s", flags.Arg(0), usage)
	}
	endpoint, err := clihttp.ParseBaseURL(*base)
	if err != nil {
		return nil, false, err
	}
	tokenFileProvided := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "token-file" {
			tokenFileProvided = true
		}
	})
	if tokenFileProvided && *tokenFile == "" {
		return nil, false, errors.New("-token-file must specify a nonempty path")
	}
	token := ""
	if *tokenFile != "" {
		if token, err = readToken(*tokenFile); err != nil {
			return nil, false, err
		}
	}
	return &client{base: endpoint, token: token, http: clihttp.NewClient()}, false, nil
}

// readToken applies mymail-cli's token-file rules.
func readToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	defer func() { _ = file.Close() }()
	tokenBytes, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	if len(tokenBytes) > 4096 {
		return "", errors.New("token input is too large")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("token input must contain one token")
	}
	return token, nil
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "devel"
}

func newServer(c *client) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "mymail-mcp", Version: version()}, &mcp.ServerOptions{
		Instructions: "Access to a MyMail server, limited to the folders the API token was granted. " +
			"Start with mymail://folders, browse a folder with mymail://folders/{folder_id}/messages and read a message " +
			"with mymail://messages/{id}/text. Use the search_messages tool to search a folder. " +
			"mark_message_read is the only change this server can make.",
		// No subscriptions, no list_changed, no logging (mcp-server.md §2.2).
		Capabilities:      &mcp.ServerCapabilities{Resources: &mcp.ResourceCapabilities{}, Tools: &mcp.ToolCapabilities{}},
		CompletionHandler: c.complete,
	})
	server.AddResource(&mcp.Resource{URI: "mymail://folders", Name: "folders", MIMEType: "application/json",
		Description: "The folders this token can read, with unread counts."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: messagesTemplate, Name: "folder-messages", MIMEType: "application/json",
		Description: "A page of a folder's messages, newest first. limit is 1-200 (default 50); unread and flagged are true or false."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "mymail://messages/{id}", Name: "message", MIMEType: "application/json",
		Description: "One message: addresses, subject, date, plain-text and HTML bodies, attachments."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "mymail://messages/{id}/text", Name: "message-text", MIMEType: "text/plain",
		Description: "A message's plain-text body."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "mymail://messages/{id}/headers", Name: "message-headers", MIMEType: "text/plain",
		Description: "A message's header block, as received."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "mymail://messages/{id}/body{?external}", Name: "message-body", MIMEType: "text/html",
		Description: "A message's sanitized HTML body. external=1 keeps external HTTPS images."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "mymail://messages/{id}/raw", Name: "message-raw", MIMEType: "message/rfc822",
		Description: "The original RFC 5322 message. Drafts have none."}, c.read)
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "mymail://attachments/{id}", Name: "attachment",
		Description: "An attachment's bytes. Its file name and type are in the message's attachments list."}, c.read)
	mcp.AddTool(server, &mcp.Tool{
		Name: "search_messages",
		Description: "Full-text search over sender, recipients, subject and plain-text body, in one folder. " +
			"sort is relevance (default), date_desc or date_asc.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)},
	}, c.searchMessages)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "mark_message_read",
		Description: "Mark a message as read. Changes nothing else; marking a read message again has no effect.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)},
	}, c.markMessageRead)
	return server
}

// client is an HTTP client of one MyMail server.
type client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// apiError is a non-2xx response.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

// do sends one API request, refusing a response body over maxBody.
func (c *client) do(ctx context.Context, method, resource string, query url.Values) (contentType string, body []byte, err error) {
	request, err := http.NewRequestWithContext(ctx, method, clihttp.APIURL(c.base, resource, query), nil)
	if err != nil {
		return "", nil, err
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	request.Header.Set("Accept", "*/*")
	response, err := c.http.Do(request)
	if err != nil {
		return "", nil, errors.New(c.redact("request failed: " + err.Error()))
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", nil, c.statusError(response)
	}
	body, err = io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxBody {
		return "", nil, fmt.Errorf("response exceeds the %d-byte (1 MiB) limit", maxBody)
	}
	// A server or proxy echoing the Authorization header must not put the
	// secret in front of the client (mcp-server.md §2.1). Error text is
	// redacted, but a successful body is refused whole: its bytes are passed
	// through unchanged or not at all.
	if c.containsToken(body) {
		return "", nil, errors.New("the response contains the API token, so it is not passed on")
	}
	return response.Header.Get("Content-Type"), body, nil
}

func (c *client) statusError(response *http.Response) error {
	switch {
	case response.StatusCode == http.StatusUnauthorized && c.token != "":
		return &apiError{response.StatusCode, "the API token is expired or revoked; run mymail-token login"}
	case response.StatusCode == http.StatusUnauthorized:
		return &apiError{response.StatusCode, "the server requires authentication; set MYMAIL_TOKEN_FILE or pass -token-file"}
	case response.StatusCode == http.StatusForbidden:
		// The bearer middleware cannot tell a message in another folder from
		// one that does not exist, and answers both with 403.
		return &apiError{response.StatusCode, "outside this token's scope, or it does not exist"}
	}
	// The error body is held to the same cap as any other: its error field is
	// carried whole or not at all, never cut short. The API's own errors use
	// `error`; ogen's request-decoding errors use `error_message`.
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	message := fmt.Sprintf("HTTP %d", response.StatusCode)
	switch detail := errorField(raw); {
	case err != nil || len(raw) > maxBody:
	case detail != "":
		message += ": " + detail
	case len(raw) <= 1024 && utf8.Valid(raw) && strings.TrimSpace(string(raw)) != "" && !c.containsToken(raw):
		// Not the API's error shape, perhaps a proxy's; short enough to show whole.
		message += ": " + strings.TrimSpace(string(raw))
	}
	return &apiError{response.StatusCode, c.redact(message)}
}

// errorField returns the first of `error` and `error_message` that is a
// nonempty string. Each is decoded on its own, so a field of another type
// cannot hide the other.
func errorField(raw []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	for _, name := range []string{"error", "error_message"} {
		var value string
		if json.Unmarshal(fields[name], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

// containsToken reports whether body carries the token, either as its bytes
// or, in JSON, as a string that decodes to it: `\u0072…` is the same secret to
// anything that parses the body. Every string token is checked, keys and
// duplicate members included, since a decoded map keeps only the last of a
// repeated key.
func (c *client) containsToken(body []byte) bool {
	if c.token == "" {
		return false
	}
	if bytes.Contains(body, []byte(c.token)) {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if text, ok := token.(string); ok && strings.Contains(text, c.token) {
			return true
		}
	}
}

// redact removes the token from text that came from elsewhere — a server or
// proxy echoing the Authorization header must not put it in front of the
// client (mcp-server.md §2.1).
func (c *client) redact(text string) string {
	if c.token == "" {
		return text
	}
	return strings.ReplaceAll(text, c.token, "[token]")
}

// refuse rejects a resource URI this server will not forward. It uses the
// internal-error code, not invalid params: go-sdk v1.8.0 gives "Resource not
// found" the invalid-params code, and a client that shows every -32602 as not
// found would hide the message (mcp-server.md §5.8).
func refuse(format string, args ...any) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: fmt.Sprintf(format, args...)}
}

// target is the API request a resource URI names.
type target struct {
	resource string
	query    url.Values
	blob     bool // always a blob, whatever the response (mcp-server.md §2.4)
	text     bool // the message's body_text, not the response itself
	raw      bool // a raw message, which a draft does not have
}

// resolve maps a resource URI to its API request. The SDK has already chosen
// this server by matching a template, but its matcher neither extracts
// reordered or repeated query parameters nor rejects unknown ones, so the URI
// is parsed here (mcp-server.md §3).
func resolve(raw string) (target, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "mymail" || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return target{}, refuse("invalid resource URI %q", raw)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return target{}, refuse("invalid query in %q", raw)
	}
	var segments []string
	if path := strings.TrimPrefix(u.EscapedPath(), "/"); path != "" {
		for _, escaped := range strings.Split(path, "/") {
			segment, err := url.PathUnescape(escaped)
			if err != nil || segment == "" {
				return target{}, refuse("invalid resource URI %q", raw)
			}
			segments = append(segments, segment)
		}
	}
	// only accepts the named query parameters, each at most once.
	only := func(t target, allowed ...string) (target, error) {
		if u.ForceQuery && u.RawQuery == "" {
			return target{}, refuse("%q has an empty query", raw)
		}
		for key, values := range query {
			if !slices.Contains(allowed, key) {
				if len(allowed) == 0 {
					return target{}, refuse("%q takes no query parameters", raw)
				}
				return target{}, refuse("unknown query parameter %q; use the search_messages tool to search", key)
			}
			if len(values) != 1 {
				return target{}, refuse("%s may appear only once", key)
			}
		}
		t.query = query
		return t, nil
	}
	if len(segments) > 0 {
		if id, err := strconv.ParseInt(segments[0], 10, 64); err != nil || id <= 0 || strconv.FormatInt(id, 10) != segments[0] {
			return target{}, refuse("%q: the ID must be a positive integer", raw)
		}
	}
	switch {
	case u.Host == "folders" && len(segments) == 0:
		return only(target{resource: "/folders"})
	case u.Host == "folders" && len(segments) == 2 && segments[1] == "messages":
		return only(target{resource: "/folders/" + segments[0] + "/messages"}, "limit", "offset", "unread", "flagged")
	case u.Host == "messages" && len(segments) == 1:
		return only(target{resource: "/messages/" + segments[0]})
	case u.Host == "messages" && len(segments) == 2 && segments[1] == "text":
		return only(target{resource: "/messages/" + segments[0], text: true})
	case u.Host == "messages" && len(segments) == 2 && segments[1] == "headers":
		return only(target{resource: "/messages/" + segments[0] + "/headers"})
	case u.Host == "messages" && len(segments) == 2 && segments[1] == "body":
		return only(target{resource: "/messages/" + segments[0] + "/body"}, "external")
	case u.Host == "messages" && len(segments) == 2 && segments[1] == "raw":
		return only(target{resource: "/messages/" + segments[0] + "/raw", blob: true, raw: true})
	case u.Host == "attachments" && len(segments) == 1:
		return only(target{resource: "/attachments/" + segments[0], blob: true})
	}
	return target{}, mcp.ResourceNotFoundError(raw)
}

func (c *client) read(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := request.Params.URI
	t, err := resolve(uri)
	if err != nil {
		return nil, err
	}
	contentType, body, err := c.do(ctx, http.MethodGet, t.resource, t.query)
	if err != nil {
		if apiErr, ok := errors.AsType[*apiError](err); ok && apiErr.status == http.StatusNotFound {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: err.Error()}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "application/octet-stream"
	}
	switch {
	case t.raw && mediaType == "application/json":
		// The API answers a draft's raw message with 200 and `{}`.
		return nil, refuse("%q is a draft, which has no raw message", uri)
	case t.text:
		var message struct {
			BodyText string `json:"body_text"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "invalid message from server"}
		}
		body, mediaType = []byte(message.BodyText), "text/plain"
	}
	contents := &mcp.ResourceContents{URI: uri, MIMEType: mediaType}
	textual := strings.HasPrefix(mediaType, "text/") || mediaType == "application/json"
	// go-sdk v1.8.0 drops an empty text field from the wire but keeps an empty,
	// non-nil blob, so an empty body is always an empty blob (mcp-server.md §2.4).
	if !t.blob && textual && utf8.Valid(body) && len(body) > 0 {
		contents.Text = string(body)
	} else {
		contents.Blob = append([]byte{}, body...)
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{contents}}, nil
}

type searchMessagesInput struct {
	FolderID int64  `json:"folder_id" jsonschema:"the folder to search; a token can search only its own folders, one at a time"`
	Q        string `json:"q" jsonschema:"search text"`
	DateFrom string `json:"date_from,omitempty" jsonschema:"inclusive lower bound on the message date, RFC 3339"`
	DateTo   string `json:"date_to,omitempty" jsonschema:"exclusive upper bound on the message date, RFC 3339"`
	FromAddr string `json:"from_addr,omitempty" jsonschema:"substring of the sender"`
	ToAddr   string `json:"to_addr,omitempty" jsonschema:"substring of a recipient"`
	Sort     string `json:"sort,omitempty" jsonschema:"relevance (default), date_desc or date_asc"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"page size, 1-200, default 50"`
	Offset   *int   `json:"offset,omitempty" jsonschema:"number of results to skip"`
}

func (c *client) searchMessages(ctx context.Context, _ *mcp.CallToolRequest, in searchMessagesInput) (*mcp.CallToolResult, any, error) {
	query := url.Values{"folder_id": {strconv.FormatInt(in.FolderID, 10)}, "q": {in.Q}}
	for name, value := range map[string]string{"date_from": in.DateFrom, "date_to": in.DateTo, "from_addr": in.FromAddr, "to_addr": in.ToAddr, "sort": in.Sort} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if in.Limit != nil {
		query.Set("limit", strconv.Itoa(*in.Limit))
	}
	if in.Offset != nil {
		query.Set("offset", strconv.Itoa(*in.Offset))
	}
	_, body, err := c.do(ctx, http.MethodGet, "/messages/search", query)
	if err != nil {
		return nil, nil, err
	}
	// The text block is the server's bytes; left unset, the SDK would fill it
	// by re-marshalling the structured output.
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, json.RawMessage(body), nil
}

type markMessageReadInput struct {
	ID int64 `json:"id" jsonschema:"the message to mark as read"`
}

func (c *client) markMessageRead(ctx context.Context, _ *mcp.CallToolRequest, in markMessageReadInput) (*mcp.CallToolResult, any, error) {
	if in.ID <= 0 {
		return nil, nil, errors.New("id must be a positive integer")
	}
	if _, _, err := c.do(ctx, http.MethodPut, "/messages/"+strconv.FormatInt(in.ID, 10)+"/read", nil); err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Message %d is marked as read.", in.ID)}}}, nil, nil
}

// complete offers folder IDs for the messages template's folder_id argument,
// and nothing for any other argument (mcp-server.md §2.6).
func (c *client) complete(ctx context.Context, request *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	result := &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: []string{}}}
	ref := request.Params.Ref
	if ref == nil || ref.Type != "ref/resource" || ref.URI != messagesTemplate || request.Params.Argument.Name != "folder_id" {
		return result, nil
	}
	_, body, err := c.do(ctx, http.MethodGet, "/folders", nil)
	if err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: err.Error()}
	}
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "invalid folder list from server"}
	}
	for _, folder := range list.Items {
		if id := strconv.FormatInt(folder.ID, 10); strings.HasPrefix(id, request.Params.Argument.Value) {
			result.Completion.Values = append(result.Completion.Values, id)
		}
	}
	// The protocol caps a completion at 100 values.
	if len(result.Completion.Values) > 100 {
		result.Completion.Total = len(result.Completion.Values)
		result.Completion.HasMore = true
		result.Completion.Values = result.Completion.Values[:100]
	}
	return result, nil
}
