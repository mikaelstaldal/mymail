package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/mikaelstaldal/go-server-common/devicegrant/client"
	"github.com/mikaelstaldal/go-server-common/termsafe"
	"github.com/mikaelstaldal/mymail/internal/clihttp"
)

func login(args []string, base string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "", "token name")
	lifetime := flags.String("lifetime", "30d", "requested lifetime")
	resources := flags.String("folders", "", "requested folders")
	out := flags.String("out", "", "write token to a new private file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*name) == "" || len(*name) > 200 || !utf8.ValidString(*name) || strings.IndexFunc(*name, func(c rune) bool { return unicode.IsControl(c) || unicode.Is(unicode.Cf, c) }) >= 0 {
		return errors.New("login needs a name of 1–200 bytes without control characters")
	}
	if _, err := parseLifetime(*lifetime); err != nil {
		return err
	}
	scope := []string{}
	if *resources != "" {
		ids, err := parseFolders(*resources)
		if err != nil {
			return err
		}
		for _, id := range ids {
			scope = append(scope, fmt.Sprintf("folder:%d", id))
		}
	}
	endpoint, err := clihttp.ParseBaseURL(base)
	if err != nil {
		return err
	}
	output, err := newTokenOutput(*out, stdout)
	if err != nil {
		return err
	}
	defer output.cleanup()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	token, err := client.Login(ctx, clihttp.NewClient(), clihttp.APIURL(endpoint, "/oauth/device_authorization", nil), clihttp.APIURL(endpoint, "/oauth/token", nil), "mymail-token", *name, strings.Join(scope, " "), *lifetime, func(a client.Authorization) error {
		if strings.IndexFunc(a.UserCode+a.VerificationURI+a.VerificationURIComplete, unicode.IsControl) >= 0 {
			return errors.New("invalid approval details")
		}
		uri := a.VerificationURIComplete
		if uri == "" {
			uri = a.VerificationURI
		}
		_, err := fmt.Fprintf(stderr, "Open %s\nUser code: %s\nApprove this request on the page above.\n", termsafe.String(uri), termsafe.String(a.UserCode))
		return err
	})
	if err != nil {
		return err
	}
	if !tokenPattern.MatchString(token.AccessToken) || !slugPattern.MatchString(token.Slug) {
		return errors.New("server response has no valid token and slug")
	}
	if _, err = fmt.Fprintf(stderr, "Token slug: %s\n", token.Slug); err != nil {
		return err
	}
	return output.write(token.AccessToken)
}
