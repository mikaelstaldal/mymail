// Command cli is the script-oriented client for the MyMail API.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikaelstaldal/mymail/internal/clihttp"
)

const usage = `Usage: mymail-cli [global flags] <command> [command flags]

Global flags (before the command):
  -help             Print usage and exit successfully
  -url URL          Server base URL, including optional path (default MYMAIL_URL or http://127.0.0.1:8080)
  -token-file PATH  Read API token from a file (default MYMAIL_TOKEN_FILE)
  -token-stdin      Read API token from standard input

Token flags are optional and mutually exclusive; either overrides MYMAIL_TOKEN_FILE.
Without a token flag or MYMAIL_TOKEN_FILE, no Authorization header is sent.

Commands:
  folders list
  messages list FOLDER_ID [-limit N] [-offset N] [-unread] [-flagged]
  messages search -folder ID -q TEXT [-limit N] [-offset N]
                  [-sort relevance|date_asc|date_desc] [-date-from RFC3339]
                  [-date-to RFC3339] [-from ADDR] [-to ADDR]
  messages get ID
  messages text ID
  messages raw ID
  messages headers ID
  messages body ID [-external]
  messages read ID
  attachments get ID

JSON responses and downloaded bytes go to stdout unchanged; messages text writes
only the stored plain-text body, without adding a newline. Errors go to stderr.
Only token-accessible API routes are exposed. Search always requires a folder.
HTTP is allowed only for literal loopback addresses; remote servers require HTTPS.
Redirects and environment HTTP proxies are disabled.
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mymail-cli:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("mymail-cli", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	base := flags.String("url", clihttp.URLDefault(), "server base URL")
	tokenFile := flags.String("token-file", os.Getenv("MYMAIL_TOKEN_FILE"), "token file")
	tokenStdin := flags.Bool("token-stdin", false, "read token from stdin")
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
	method, path, query, err := parseCommand(command)
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
	tokenFileProvided := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "token-file" {
			tokenFileProvided = true
		}
	})
	if tokenFileProvided && *tokenStdin {
		return errors.New("specify at most one of -token-file or -token-stdin")
	}
	if tokenFileProvided && *tokenFile == "" {
		return errors.New("-token-file must specify a nonempty path")
	}
	token := ""
	if *tokenFile != "" || *tokenStdin {
		tokenReader := stdin
		if !*tokenStdin {
			file, openErr := os.Open(*tokenFile)
			if openErr != nil {
				return fmt.Errorf("read token: %w", openErr)
			}
			defer func() { _ = file.Close() }()
			tokenReader = file
		}
		tokenBytes, err := io.ReadAll(io.LimitReader(tokenReader, 4097))
		if err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		if len(tokenBytes) > 4096 {
			return errors.New("token input is too large")
		}
		token = strings.TrimSpace(string(tokenBytes))
		if token == "" || strings.ContainsAny(token, " \t\r\n") {
			return errors.New("token input must contain one token")
		}
	}
	request, err := http.NewRequest(method, clihttp.APIURL(endpoint, path, query), nil)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Accept", "application/json")
	client := clihttp.NewClient()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if command[0] == "messages" && command[1] == "text" {
		var message struct {
			BodyText string `json:"body_text"`
		}
		if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
			return fmt.Errorf("decode message: %w", err)
		}
		_, err = io.WriteString(stdout, message.BodyText)
		return err
	}
	_, err = io.Copy(stdout, response.Body)
	return err
}

func parseCommand(args []string) (string, string, url.Values, error) {
	bad := func() (string, string, url.Values, error) {
		return "", "", nil, fmt.Errorf("invalid command or arguments\n%s", usage)
	}
	if len(args) < 2 {
		return bad()
	}
	// Recognize help in place of an ID, and for commands without flag parsers.
	command := args[0] + " " + args[1]
	switch command {
	case "folders list", "attachments get", "messages list", "messages search",
		"messages get", "messages text", "messages raw", "messages headers",
		"messages body", "messages read":
		if len(args) == 3 && isHelp(args[2]) {
			return "", "", nil, flag.ErrHelp
		}
		if command != "messages search" && len(args) == 4 && isHelp(args[3]) {
			return "", "", nil, flag.ErrHelp
		}
	}
	query := url.Values{}
	switch args[0] {
	case "folders":
		if len(args) == 2 && args[1] == "list" {
			return http.MethodGet, "/folders", query, nil
		}
	case "attachments":
		if len(args) == 3 && args[1] == "get" {
			id, err := positiveID(args[2])
			if err != nil {
				return "", "", nil, err
			}
			return http.MethodGet, "/attachments/" + id, query, nil
		}
	case "messages":
		if args[1] == "search" {
			fs := commandFlags("messages search")
			folder := fs.String("folder", "", "required folder ID")
			q := fs.String("q", "", "search text")
			limit := fs.Int("limit", 50, "page size")
			offset := fs.Int("offset", 0, "page offset")
			sort := fs.String("sort", "", "sort order")
			dateFrom := fs.String("date-from", "", "inclusive RFC3339 timestamp")
			dateTo := fs.String("date-to", "", "exclusive RFC3339 timestamp")
			from := fs.String("from", "", "sender substring")
			to := fs.String("to", "", "recipient substring")
			if err := fs.Parse(args[2:]); err != nil {
				return "", "", nil, err
			}
			if fs.NArg() != 0 {
				return bad()
			}
			id, err := positiveID(*folder)
			if err != nil {
				return "", "", nil, fmt.Errorf("-folder: %w", err)
			}
			if strings.TrimSpace(*q) == "" || utf8.RuneCountInString(*q) > 500 {
				return "", "", nil, errors.New("-q must contain 1–500 characters of search text")
			}
			if err := page(*limit, *offset); err != nil {
				return "", "", nil, err
			}
			if *sort != "" && *sort != "relevance" && *sort != "date_asc" && *sort != "date_desc" {
				return "", "", nil, errors.New("invalid -sort")
			}
			for _, value := range []string{*dateFrom, *dateTo} {
				if value != "" {
					if _, err := time.Parse(time.RFC3339, value); err != nil {
						return "", "", nil, fmt.Errorf("invalid date: %w", err)
					}
				}
			}
			if utf8.RuneCountInString(*from) > 200 || utf8.RuneCountInString(*to) > 200 {
				return "", "", nil, errors.New("-from and -to are limited to 200 characters")
			}
			query.Set("folder_id", id)
			query.Set("q", *q)
			query.Set("limit", strconv.Itoa(*limit))
			query.Set("offset", strconv.Itoa(*offset))
			for name, value := range map[string]string{"sort": *sort, "date_from": *dateFrom, "date_to": *dateTo, "from_addr": *from, "to_addr": *to} {
				if value != "" {
					query.Set(name, value)
				}
			}
			return http.MethodGet, "/messages/search", query, nil
		}
		if len(args) < 3 {
			return bad()
		}
		id, err := positiveID(args[2])
		if err != nil {
			return "", "", nil, err
		}
		switch args[1] {
		case "list":
			fs := commandFlags("messages list")
			limit := fs.Int("limit", 50, "page size")
			offset := fs.Int("offset", 0, "page offset")
			unread := fs.Bool("unread", false, "unread only")
			flagged := fs.Bool("flagged", false, "flagged only")
			if err := fs.Parse(args[3:]); err != nil {
				return "", "", nil, err
			}
			if fs.NArg() != 0 {
				return bad()
			}
			if err := page(*limit, *offset); err != nil {
				return "", "", nil, err
			}
			query.Set("limit", strconv.Itoa(*limit))
			query.Set("offset", strconv.Itoa(*offset))
			if *unread {
				query.Set("unread", "true")
			}
			if *flagged {
				query.Set("flagged", "true")
			}
			return http.MethodGet, "/folders/" + id + "/messages", query, nil
		case "get", "text", "raw", "headers", "body", "read":
			if args[1] == "body" {
				fs := commandFlags("messages body")
				external := fs.Bool("external", false, "allow external images in HTML")
				if err := fs.Parse(args[3:]); err != nil {
					return "", "", nil, err
				}
				if fs.NArg() != 0 {
					return bad()
				}
				if *external {
					query.Set("external", "1")
				}
			} else if len(args) != 3 {
				return bad()
			}
			if args[1] == "get" || args[1] == "text" {
				return http.MethodGet, "/messages/" + id, query, nil
			}
			if args[1] == "read" {
				return http.MethodPut, "/messages/" + id + "/read", query, nil
			}
			return http.MethodGet, "/messages/" + id + "/" + args[1], query, nil
		}
	}
	return bad()
}

func commandFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func positiveID(value string) (string, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return "", errors.New("ID must be a positive integer")
	}
	return strconv.FormatInt(id, 10), nil
}

func page(limit, offset int) error {
	if limit < 1 || limit > 200 || offset < 0 {
		return errors.New("-limit must be 1–200 and -offset must be nonnegative")
	}
	return nil
}

func isHelp(arg string) bool {
	return arg == "-help" || arg == "--help" || arg == "-h"
}
