package phishing

import (
	"fmt"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainTextNormalization(t *testing.T) {
	claims := []string{"paypal.com"}
	for _, r := range []rune{'\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u202c', '\u2066', '\u061c', '\u180e', '\ufeff', '\u2060', '\u00ad'} {
		claims = append(claims, "paypal.com"+string(r), "pay"+string(r)+"pal.com", string(r)+"paypal.com")
	}
	for _, r := range []rune{'\u3002', '\uff0e', '\uff61', '\u2024', '\ufe52'} {
		claims = append(claims, "paypal"+string(r)+"com")
	}
	for _, claim := range claims {
		t.Run(fmt.Sprintf("%q", claim), func(t *testing.T) {
			for _, text := range []string{claim, "https://" + claim + "/login"} {
				body := sanitize.HTML(`<a href="https://evil.example/login">` + text + `</a>`)
				m := model.DBMessage{FolderID: 1, FromAddr: "x@evil.example", BodyHTML: body}
				warnings := ComputeWithLinks(m, nil, Context{}, Links(body))
				var deceptive *api.MessageWarning
				for i := range warnings {
					if warnings[i].Kind == "deceptive_link" {
						deceptive = &warnings[i]
					}
				}
				require.NotNil(t, deceptive, text)
				assert.Equal(t, text, deceptive.Text.Value)
				assert.Contains(t, RewriteLinks(body, 42, warnings), `href="../../../../#/link/42/0"`)
				m.BodyHTML = sanitize.HTML(`<a href="https://login.paypal.com">` + text + `</a>`)
				for _, w := range Compute(m, nil, Context{}) {
					assert.NotEqual(t, api.MessageWarningKind("deceptive_link"), w.Kind)
				}
			}
			for _, domain := range []string{"evil.example", "paypal.com"} {
				m := model.DBMessage{FolderID: 1, FromAddr: `"` + claim + ` Support" <x@` + domain + `>`}
				found := false
				for _, w := range Compute(m, nil, Context{}) {
					if w.Kind == "display_name_address" {
						found = true
					}
				}
				assert.Equal(t, domain == "evil.example", found)
			}
		})
	}
}
