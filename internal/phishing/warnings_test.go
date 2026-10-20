package phishing

import (
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWarnings(t *testing.T) {
	base := model.DBMessage{FolderID: 1, FromAddr: "Alice <alice@example.com>"}
	tests := []struct {
		name   string
		change func(*model.DBMessage)
		ctx    Context
		atts   []api.AttachmentMeta
		kinds  []api.MessageWarningKind
	}{
		{name: "explicit sender", ctx: Context{ExplicitContact: true}, kinds: []api.MessageWarningKind{}},
		{name: "explicit spoofed sender", ctx: Context{ExplicitContact: true}, change: func(m *model.DBMessage) {
			m.SenderAuthentication = `{"dmarc":"fail","dkim_aligned":false,"spf":"unknown"}`
		}, kinds: []api.MessageWarningKind{"auth_fail"}},
		{name: "new sender", kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "sent before", ctx: Context{SentTo: true}, kinds: []api.MessageWarningKind{}},
		{name: "auth failure", change: func(m *model.DBMessage) {
			m.SenderAuthentication = `{"dmarc":"fail","dkim_aligned":false,"spf":"fail"}`
		}, kinds: []api.MessageWarningKind{"auth_fail", "first_time_sender"}},
		{name: "display address", change: func(m *model.DBMessage) { m.FromAddr = `"boss@company.org" <evil@example.com>` }, kinds: []api.MessageWarningKind{"display_name_address", "first_time_sender"}},
		{name: "display domain", change: func(m *model.DBMessage) { m.FromAddr = `"company.org Support" <evil@example.com>` }, kinds: []api.MessageWarningKind{"display_name_address", "first_time_sender"}},
		{name: "display same address", change: func(m *model.DBMessage) { m.FromAddr = `"ALICE@EXAMPLE.COM" <alice@example.com>` }, kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "reply other", change: func(m *model.DBMessage) { m.ReplyToAddr = "attacker@other.org" }, kinds: []api.MessageWarningKind{"reply_to_mismatch", "first_time_sender"}},
		{name: "reply subdomain", change: func(m *model.DBMessage) { m.FromAddr = "a@mail.example.co.uk"; m.ReplyToAddr = "b@reply.example.co.uk" }, kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "deceptive", change: func(m *model.DBMessage) { m.BodyHTML = `<a>skip</a><a href="https://evil.org">https://example.com</a>` }, kinds: []api.MessageWarningKind{"first_time_sender", "deceptive_link"}},
		{name: "ordinary text", change: func(m *model.DBMessage) { m.BodyHTML = `<a href="https://example.org">View your account</a>` }, kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "link same registrable", change: func(m *model.DBMessage) { m.BodyHTML = `<a href="https://login.example.co.uk">www.example.co.uk</a>` }, kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "IP", change: func(m *model.DBMessage) { m.BodyHTML = `<a href="https://[2001:db8::1]/">click</a>` }, kinds: []api.MessageWarningKind{"first_time_sender", "deceptive_link"}},
		{name: "punycode", change: func(m *model.DBMessage) { m.FromAddr = "user@xn--bcher-kva.de" }, kinds: []api.MessageWarningKind{"lookalike_domain", "first_time_sender"}},
		{name: "mixed script", change: func(m *model.DBMessage) { m.FromAddr = "user@pаypal.com" }, ctx: Context{KnownDomains: []string{"paypal.com"}}, kinds: []api.MessageWarningKind{"lookalike_domain", "first_time_sender"}},
		{name: "ASCII skeleton", change: func(m *model.DBMessage) { m.FromAddr = "user@exarnple.com" }, ctx: Context{KnownDomains: []string{"example.com"}}, kinds: []api.MessageWarningKind{"lookalike_domain", "first_time_sender"}},
		{name: "same known domain", ctx: Context{KnownDomains: []string{"exarnple.com", "example.com"}, SentTo: true}, kinds: []api.MessageWarningKind{}},
		{name: "risky", atts: []api.AttachmentMeta{{Filename: "invoice.pdf.exe", ContentType: "application/octet-stream"}}, kinds: []api.MessageWarningKind{"first_time_sender", "risky_attachment"}},
		{name: "safe attachment", atts: []api.AttachmentMeta{{Filename: "report.pdf", ContentType: "application/pdf"}}, kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "own pass", change: func(m *model.DBMessage) {
			m.SenderAuthentication = `{"dmarc":"pass","dkim_aligned":true,"spf":"pass"}`
			m.BodyHTML = `<a href="https://evil.org">example.com</a>`
		}, ctx: Context{OwnAddresses: []string{"ALICE@example.com"}}, kinds: []api.MessageWarningKind{}},
		{name: "own unknown", ctx: Context{OwnAddresses: []string{"alice@example.com"}}, kinds: []api.MessageWarningKind{"first_time_sender"}},
		{name: "draft", change: func(m *model.DBMessage) { m.FolderID = 3 }, kinds: []api.MessageWarningKind{}},
		{name: "sent", change: func(m *model.DBMessage) { m.FolderID = 2 }, kinds: []api.MessageWarningKind{}},
		{name: "scheduled", change: func(m *model.DBMessage) { m.FolderID = 5 }, kinds: []api.MessageWarningKind{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			if tt.change != nil {
				tt.change(&m)
			}
			got := Compute(m, tt.atts, tt.ctx)
			kinds := []api.MessageWarningKind{}
			for _, w := range got {
				kinds = append(kinds, w.Kind)
			}
			assert.Equal(t, tt.kinds, kinds)
		})
	}
}
func TestWarningDetails(t *testing.T) {
	m := model.DBMessage{FolderID: 1, FromAddr: `"boss@company.org" <user@pаypal.com>`, ReplyToAddr: "x@evil.org", SenderAuthentication: `{"dmarc":"fail","dkim_aligned":false,"spf":"fail"}`, BodyHTML: `<a>skip</a><a href="https://evil.org">https://<b>paypal.com</b></a>`}
	got := Compute(m, []api.AttachmentMeta{{Filename: "invoice.pdf.exe"}}, Context{KnownDomains: []string{"paypal.com"}})
	require.Len(t, got, 7)
	assert.Equal(t, []api.MessageWarningFailedChecksItem{"dmarc", "dkim_alignment", "spf"}, got[0].FailedChecks)
	assert.Equal(t, "boss@company.org", got[1].Address.Value)
	assert.Equal(t, "pаypal.com", got[2].Domain.Value)
	assert.Equal(t, "xn--pypal-4ve.com", got[2].Punycode.Value)
	assert.Equal(t, "paypal.com", got[2].Resembles.Value)
	assert.Equal(t, "x@evil.org", got[3].Address.Value)
	assert.Equal(t, 1, got[5].LinkIndex.Value)
	assert.Equal(t, "https://paypal.com", got[5].Text.Value)
	assert.Equal(t, "https://evil.org", got[5].Href.Value)
	assert.Equal(t, api.MessageWarningReasonDoubleExtension, got[6].Reason.Value)
}
func TestLegitimateIDN(t *testing.T) {
	m := model.DBMessage{FolderID: 1, FromAddr: "a@bücher.de"}
	got := Compute(m, nil, Context{SentTo: true, KnownDomains: []string{"xn--bcher-kva.de"}})
	require.Len(t, got, 1) // IDN is advisory even for a known correspondent, never an impersonation claim.
	assert.Equal(t, api.MessageWarningKindLookalikeDomain, got[0].Kind)
	assert.False(t, got[0].Resembles.Set)
	assert.Equal(t, "bücher.de", got[0].Domain.Value)
	assert.Equal(t, Skeleton("bücher.de"), Skeleton("xn--bcher-kva.de"))
}
func TestLinksBoundedAndDocumentParsed(t *testing.T) {
	links := Links(`<a>zero</a><!-- <a href="https://fake.org">x</a> --><a href="https://evil.org?a=1&amp;b=2">example.<b>com</b></a>`)
	require.Len(t, links, 2)
	assert.Equal(t, "example.com", links[1].Text)
	assert.Equal(t, "https://evil.org?a=1&b=2", links[1].Href)
	links = Links(strings.Repeat(`<a href="https://evil.org">example.com</a>`, MaxLinks+100))
	require.Len(t, links, MaxLinks)
	assert.Equal(t, MaxLinks-1, links[MaxLinks-1].Index)
}
func TestAttachmentRisk(t *testing.T) {
	for _, v := range []struct{ name, ct, want string }{{"x.pdf", "application/x-executable", "executable_content_type"}, {"x.PS1", "application/octet-stream", "executable_extension"}, {"photo.jpeg", "image/jpeg", ""}, {"a.exe ", "", "executable_extension"}} {
		assert.Equal(t, v.want, AttachmentRisk(v.name, v.ct))
	}
}

func TestInvalidSenderCannotSuppressIndependentSignals(t *testing.T) {
	m := model.DBMessage{FolderID: 1, FromAddr: "invalid <", SenderAuthentication: `{"dmarc":"fail","dkim_aligned":false,"spf":"fail"}`, BodyHTML: `<a href="https://evil.org">example.com</a>`}
	got := Compute(m, []api.AttachmentMeta{{Filename: "bad.exe"}}, Context{})
	kinds := []api.MessageWarningKind{}
	for _, w := range got {
		kinds = append(kinds, w.Kind)
	}
	assert.Equal(t, []api.MessageWarningKind{"malformed_sender", "auth_fail", "deceptive_link", "risky_attachment"}, kinds)
}

// A sender that is not exactly one mailbox leaves the sender checks nothing to
// compare, so it must be flagged rather than pass with no warnings at all.
func TestSenderThatIsNotOneMailboxIsFlagged(t *testing.T) {
	for _, from := range []string{
		``,
		`Bank Security`,
		`"ceo@bank.com" <x..y@attacker.example>`,
		`x@evil.com, Bank <ceo@bank.com>`,
	} {
		t.Run(from, func(t *testing.T) {
			got := Compute(model.DBMessage{FolderID: 1, FromAddr: from}, nil, Context{SentTo: true, ExplicitContact: true})
			require.Len(t, got, 1)
			assert.Equal(t, api.MessageWarningKindMalformedSender, got[0].Kind)
		})
	}
}

func TestReplyToListCannotHideMismatch(t *testing.T) {
	got := Compute(model.DBMessage{FolderID: 1, FromAddr: "a@example.com", ReplyToAddr: "b@mail.example.com, thief@evil.org"}, nil, Context{SentTo: true})
	require.Len(t, got, 1)
	assert.Equal(t, api.MessageWarningKindReplyToMismatch, got[0].Kind)
	assert.Equal(t, "thief@evil.org", got[0].Address.Value)
}
func TestPunycodePrefixMustStartLabel(t *testing.T) {
	got := Compute(model.DBMessage{FolderID: 1, FromAddr: "a@prefix-xn--ordinary.com", BodyHTML: `<a href="https://prefix-xn--ordinary.com">click</a>`}, nil, Context{SentTo: true})
	assert.Empty(t, got)
}

func TestAttachmentDoubleExtensionsAndActiveContent(t *testing.T) {
	for _, name := range []string{"invoice.exe.pdf", "report.final.pdf", "archive.tar.gz"} {
		assert.Equal(t, "double_extension", AttachmentRisk(name, "application/octet-stream"))
	}
	assert.Equal(t, "executable_content_type", AttachmentRisk("download", "text/html; charset=utf-8"))
	assert.Equal(t, "executable_extension", AttachmentRisk("macro.docm", "application/octet-stream"))
}

func TestDeepMarkupCannotHideDeceptiveLink(t *testing.T) {
	body := strings.Repeat("<div>", 600) + `<a href="https://evil.org?a=1&amp;b=2">example.<b>com</b></a>` + strings.Repeat("</div>", 600)
	got := Compute(model.DBMessage{FolderID: 1, FromAddr: "a@example.com", BodyHTML: body}, nil, Context{SentTo: true})
	require.Len(t, got, 1)
	assert.Equal(t, api.MessageWarningKindDeceptiveLink, got[0].Kind)
	assert.Equal(t, 0, got[0].LinkIndex.Value)
	assert.Equal(t, "example.com", got[0].Text.Value)
	assert.Equal(t, "https://evil.org?a=1&b=2", got[0].Href.Value)
}

func TestBrowserNumericIPs(t *testing.T) {
	for href, host := range map[string]string{"http://3232235777/": "192.168.1.1", "http://0x7f.1/": "127.0.0.1", "http://127.1/": "127.0.0.1", "http://0177.0.0.1/": "127.0.0.1", "http://0xffffffff/": "255.255.255.255"} {
		t.Run(href, func(t *testing.T) {
			got := Compute(model.DBMessage{FolderID: 1, FromAddr: "a@example.com", BodyHTML: `<a href="` + href + `">click</a>`}, nil, Context{SentTo: true})
			require.Len(t, got, 1)
			assert.Equal(t, api.MessageWarningKindDeceptiveLink, got[0].Kind)
			assert.Equal(t, host, got[0].DecodedHost.Value)
		})
	}
	for _, host := range []string{"example.com", "1.example.com", "256.0.0.1", "4294967296", "127.08"} {
		assert.Empty(t, browserIP(host))
	}
}
func TestFilenameTextIsNotAURL(t *testing.T) {
	for _, text := range []string{"report.pdf", "v1.2", "3.14"} {
		got := Compute(model.DBMessage{FolderID: 1, FromAddr: "a@example.com", BodyHTML: `<a href="https://example.com">` + text + `</a>`}, nil, Context{SentTo: true})
		assert.Empty(t, got, text)
	}
	for _, from := range []string{`"St.John Smith" <john@example.com>`, `"Example.com Support" <noreply@mail.example.com>`} {
		assert.Empty(t, Compute(model.DBMessage{FolderID: 1, FromAddr: from}, nil, Context{SentTo: true}), from)
	}
}
func TestLookalikeRegistrableDomains(t *testing.T) {
	got := Compute(model.DBMessage{FolderID: 1, FromAddr: "user@paypa1.com"}, nil, Context{SentTo: true, KnownDomains: []string{"mail.paypal.com"}})
	require.Len(t, got, 1)
	assert.Equal(t, "paypal.com", got[0].Resembles.Value)
	assert.Empty(t, Compute(model.DBMessage{FolderID: 1, FromAddr: "user@other.paypal.com"}, nil, Context{SentTo: true, KnownDomains: []string{"mail.paypal.com"}}))
}
func TestWindowsTrailingDotAttachment(t *testing.T) {
	assert.Equal(t, "executable_extension", AttachmentRisk("invoice.exe.", "application/octet-stream"))
}

func TestWildcardOnlyICANNTLDText(t *testing.T) {
	for _, text := range []string{"mybank.com.kh", "bank.com.np", "account.bank.ck"} {
		got := Compute(model.DBMessage{FolderID: 1, FromAddr: "a@example.com", BodyHTML: `<a href="https://evil.org">` + text + `</a>`}, nil, Context{SentTo: true})
		require.Len(t, got, 1, text)
		assert.Equal(t, api.MessageWarningKindDeceptiveLink, got[0].Kind, text)
	}
}

func TestExplicitContactSuppressesOnlyFirstTimeWarning(t *testing.T) {
	m := model.DBMessage{FolderID: 1, FromAddr: `"boss@company.org" <user@pаypal.com>`,
		ReplyToAddr: "x@evil.org", SenderAuthentication: `{"dmarc":"fail","dkim_aligned":false,"spf":"fail"}`,
		BodyHTML: `<a href="https://evil.org">https://paypal.com</a>`}
	warnings := Compute(m, []api.AttachmentMeta{{Filename: "invoice.pdf.exe"}}, Context{ExplicitContact: true, KnownDomains: []string{"paypal.com"}})
	kinds := []api.MessageWarningKind{}
	for _, warning := range warnings {
		kinds = append(kinds, warning.Kind)
	}
	assert.Equal(t, []api.MessageWarningKind{"auth_fail", "display_name_address", "lookalike_domain", "reply_to_mismatch", "deceptive_link", "risky_attachment"}, kinds)
}

// A bidi override inside the addr-spec reverses what follows it even inside an
// isolating <bdi>, so `\u202emoc.lapyap@ecivres` displays as service@paypal.com.
func TestFormatCharacterInSenderAddressIsFlagged(t *testing.T) {
	for _, from := range []string{
		"PayPal <\u202emoc.lapyap@ecivres>",
		"Support <service@\u202emoc.lapyap>",
		"\u2067service@paypal.com\u2069@evil.example",
		"service\u200b@paypal.com",
		"a\u2028b@example.com",
	} {
		t.Run(from, func(t *testing.T) {
			got := Compute(model.DBMessage{FolderID: 1, FromAddr: from}, nil, Context{SentTo: true, ExplicitContact: true})
			require.NotEmpty(t, got)
			assert.Equal(t, api.MessageWarningKindMalformedSender, got[0].Kind)
		})
	}
	// A format character in the display name is isolated by <bdi> and is no
	// reason to doubt the address.
	assert.Empty(t, Compute(model.DBMessage{FolderID: 1, FromAddr: "\u202eLapyaP <service@paypal.com>"}, nil, Context{SentTo: true, ExplicitContact: true}))
}
