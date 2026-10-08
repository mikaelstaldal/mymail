// Command token manages MyMail API tokens with Basic authentication.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mikaelstaldal/mymail/internal/clihttp"
	"golang.org/x/term"
)

const usage = `Usage: mymail-token [global flags] <command> [command flags]

Global flags (before the command):
  -help                   Print usage and exit successfully
  -url URL                Server base URL, including optional path (default MYMAIL_URL or http://127.0.0.1:8080)
  -user USER              Basic username (default MYMAIL_USER); prompt for password
  -credentials-file PATH  Read one username:password line from a file
  -credentials-stdin      Read one username:password line from standard input

Commands:
  create -name NAME -lifetime NUMBER[s|m|h|d] -folders ID[,ID...]
  revoke SLUG

Global flags go before the command. Credentials are optional for servers without Basic auth.
Create prints only the new token to stdout and its revocation slug to stderr.
Revoke prints confirmation to stderr. Errors produce a nonzero exit status.
HTTP is allowed only for literal loopback addresses; remote servers require HTTPS.
Redirects and environment HTTP proxies are disabled.
`

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var lifetimePattern = regexp.MustCompile(`^([1-9][0-9]*)([smhd])$`)
var tokenPattern = regexp.MustCompile(`^mymail_[A-Za-z0-9_-]{43}$`)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mymail-token:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mymail-token", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	base := flags.String("url", clihttp.URLDefault(), "server base URL")
	user := flags.String("user", os.Getenv("MYMAIL_USER"), "Basic username")
	credentialsFile := flags.String("credentials-file", "", "Basic credentials file")
	credentialsStdin := flags.Bool("credentials-stdin", false, "read Basic credentials from stdin")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, usage)
			return nil
		}
		return fmt.Errorf("%w\n%s", err, usage)
	}
	command := flags.Args()
	if len(command) == 0 || command[0] == "help" {
		_, _ = io.WriteString(stdout, usage)
		return nil
	}
	method, path, body, err := parseCommand(command, time.Now().UTC())
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, usage)
			return nil
		}
		return err
	}
	endpoint, err := clihttp.ParseBaseURL(*base)
	if err != nil {
		return err
	}
	if *credentialsFile != "" && *credentialsStdin {
		return errors.New("specify only one of -credentials-file or -credentials-stdin")
	}
	var userExplicit bool
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "user" {
			userExplicit = true
		}
	})
	if userExplicit && (*credentialsFile != "" || *credentialsStdin) {
		return errors.New("use -user or a credentials source, not both")
	}
	var username, password string
	var useBasic bool
	if *credentialsFile != "" || *credentialsStdin {
		reader := stdin
		if !*credentialsStdin {
			file, openErr := os.Open(*credentialsFile)
			if openErr != nil {
				return fmt.Errorf("read credentials: %w", openErr)
			}
			defer func() { _ = file.Close() }()
			reader = file
		}
		username, password, err = readCredentials(reader, stderr)
		if err != nil {
			return err
		}
		useBasic = true
	} else if *user != "" {
		if strings.ContainsRune(*user, ':') || strings.IndexFunc(*user, unicode.IsControl) >= 0 {
			return errors.New("-user must be a username without a colon or control characters")
		}
		username = *user
		password, err = readPassword(stdin, stderr)
		if err != nil {
			return err
		}
		useBasic = true
	}
	request, err := http.NewRequest(method, clihttp.APIURL(endpoint, path, nil), bytes.NewReader(body))
	if err != nil {
		return err
	}
	if useBasic {
		request.SetBasicAuth(username, password)
	}
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := clihttp.NewClient().Do(request)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if (method == http.MethodPost && response.StatusCode != http.StatusCreated) ||
		(method == http.MethodDelete && response.StatusCode != http.StatusNoContent) {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if method == http.MethodDelete {
		_, err = fmt.Fprintf(stderr, "Revoked token %s\n", command[1])
		return err
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > 65536 {
		return errors.New("server response is too large")
	}
	var created struct {
		Slug  string `json:"slug"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(responseBody, &created); err != nil || !slugPattern.MatchString(created.Slug) || !tokenPattern.MatchString(created.Token) {
		return errors.New("server response has no valid token and slug")
	}
	if _, err := fmt.Fprintf(stderr, "Token slug: %s\n", created.Slug); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, created.Token)
	return err
}

func readInput(reader io.Reader, stderr io.Writer, prompt string) (string, error) {
	var value []byte
	var err error
	if file, ok := reader.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		_, _ = io.WriteString(stderr, prompt)
		value, err = term.ReadPassword(int(file.Fd()))
		_, _ = io.WriteString(stderr, "\n")
	} else {
		value, err = io.ReadAll(io.LimitReader(reader, 4097))
	}
	if err != nil {
		return "", fmt.Errorf("read credentials: %w", err)
	}
	if len(value) > 4096 {
		return "", errors.New("credentials input is too large")
	}
	line := strings.TrimSuffix(string(value), "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func readCredentials(reader io.Reader, stderr io.Writer) (string, string, error) {
	line, err := readInput(reader, stderr, "Username:password: ")
	if err != nil {
		return "", "", err
	}
	username, password, found := strings.Cut(line, ":")
	if !found || username == "" || password == "" || strings.ContainsAny(line, "\x00\r\n") {
		return "", "", errors.New("credentials must be one username:password line")
	}
	for _, char := range line {
		if char < 32 || char == 127 {
			return "", "", errors.New("credentials must not contain control characters")
		}
	}
	return username, password, nil
}

func readPassword(reader io.Reader, stderr io.Writer) (string, error) {
	password, err := readInput(reader, stderr, "Password: ")
	if err != nil {
		return "", err
	}
	if password == "" || strings.IndexFunc(password, unicode.IsControl) >= 0 {
		return "", errors.New("password must be one nonempty line without control characters")
	}
	return password, nil
}

func parseCommand(args []string, now time.Time) (string, string, []byte, error) {
	bad := func() (string, string, []byte, error) {
		return "", "", nil, fmt.Errorf("invalid command or arguments\n%s", usage)
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		name := flags.String("name", "", "token name")
		lifetime := flags.String("lifetime", "", "token lifetime")
		folders := flags.String("folders", "", "comma-separated folder IDs")
		if err := flags.Parse(args[1:]); err != nil {
			return "", "", nil, err
		}
		if flags.NArg() != 0 || strings.TrimSpace(*name) == "" || len(*name) > 200 || strings.IndexFunc(*name, unicode.IsControl) >= 0 {
			return "", "", nil, errors.New("create needs a name of 1–200 bytes without control characters")
		}
		duration, err := parseLifetime(*lifetime)
		if err != nil {
			return "", "", nil, err
		}
		folderIDs, err := parseFolders(*folders)
		if err != nil {
			return "", "", nil, err
		}
		payload, err := json.Marshal(struct {
			Name      string  `json:"name"`
			ExpiresAt string  `json:"expires_at"`
			FolderIDs []int64 `json:"folder_ids"`
		}{*name, now.Add(duration).Format(time.RFC3339), folderIDs})
		return http.MethodPost, "/tokens", payload, err
	case "revoke":
		if (len(args) == 2 || len(args) == 3) && (args[len(args)-1] == "-help" || args[len(args)-1] == "--help" || args[len(args)-1] == "-h") {
			return "", "", nil, flag.ErrHelp
		}
		if len(args) != 2 || !slugPattern.MatchString(args[1]) {
			return bad()
		}
		return http.MethodDelete, "/tokens/" + args[1], nil, nil
	default:
		return bad()
	}
}

func parseLifetime(value string) (time.Duration, error) {
	parts := lifetimePattern.FindStringSubmatch(value)
	if parts == nil {
		return 0, errors.New("-lifetime must be a positive number followed by s, m, h, or d")
	}
	amount, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, errors.New("invalid -lifetime")
	}
	secondsPerUnit := map[string]int64{"s": 1, "m": 60, "h": 3600, "d": 86400}[parts[2]]
	if amount > 315360000/secondsPerUnit {
		return 0, errors.New("-lifetime exceeds ten years")
	}
	return time.Duration(amount*secondsPerUnit) * time.Second, nil
}

func parseFolders(value string) ([]int64, error) {
	if value == "" {
		return nil, errors.New("-folders needs comma-separated positive IDs")
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, part := range strings.Split(value, ",") {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 || seen[id] || strconv.FormatInt(id, 10) != part {
			return nil, errors.New("-folders needs unique comma-separated positive IDs")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}
