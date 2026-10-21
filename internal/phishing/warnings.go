// Package phishing computes advisory signals, never a safety verdict.
package phishing

import (
	"embed"
	"encoding/json"
	"mime"
	"net"
	"net/mail"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/attachmentname"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
	"github.com/mikaelstaldal/mymail/internal/senderauth"
	"golang.org/x/net/html"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

//go:embed confusables.json
var data embed.FS
var confusables = func() map[string]string {
	b, err := data.ReadFile("confusables.json")
	if err != nil {
		panic(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	return m
}()

const MaxLinks = 1000

// Domain canonicalizes both U-labels and A-labels. Equality always uses ASCII.
func Domain(s string) string {
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	if ip := browserIP(s); ip != "" {
		return ip
	}
	a, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return s
	}
	return a
}
func decoded(s string) string {
	u, err := idna.Lookup.ToUnicode(s)
	if err != nil {
		return s
	}
	return u
}
func Skeleton(domain string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(decoded(Domain(domain))) {
		if v, ok := confusables[string(r)]; ok {
			b.WriteString(v)
		} else {
			b.WriteRune(r)
		}
	}
	return norm.NFD.String(b.String())
}
func Registrable(host string) string {
	host = Domain(host)
	if net.ParseIP(host) != nil {
		return host
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return d
}

// HasFormatChar reports whether s contains an invisible formatting character:
// Unicode category Cf (bidi overrides and isolates, zero-width characters,
// U+061C) or a line or paragraph separator. None belongs in an addr-spec, and
// a bidi override there makes `\u202emoc.lapyap@ecivres` display as
// service@paypal.com even inside an isolating <bdi>.
func HasFormatChar(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
	})
}

func addressDomain(addr string) string { _, d, _ := strings.Cut(addr, "@"); return Domain(d) }

// FoldAddress is the one address normalization for identities, contacts and
// sent-to history: Unicode full case folding, except that a non-ASCII rune
// which is only a compatibility variant of ASCII (its fold and its NFKC form
// are both ASCII) is kept. The Kelvin sign K (U+212A), long s ſ (U+017F) and
// the ﬀ..ﬆ ligatures would otherwise make bob@banK.com the same correspondent
// as bob@bank.com. ß and ẞ are letters in their own right and still fold to ss.
func FoldAddress(addr string) string {
	var b strings.Builder
	for _, r := range addr {
		b.WriteString(FoldRune(r))
	}
	return b.String()
}

// FoldRune is FoldAddress for a single rune; the generated browser table is
// built from it.
func FoldRune(r rune) string {
	s := string(r)
	f := cases.Fold().String(s)
	if r >= utf8.RuneSelf && isASCII(f) && isASCII(norm.NFKC.String(s)) {
		return s
	}
	return f
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// mapsToASCII reports whether host, as received, holds a non-ASCII character
// that IDNA mapping turns into ASCII or removes: a compatibility form such as
// the Kelvin sign, long s, fullwidth or mathematical letters, or a soft hyphen.
// Domain() would then return a plain ASCII host indistinguishable from the one
// it imitates. Ordinary IDN characters map to an xn-- label instead.
func mapsToASCII(host string) bool {
	for _, r := range host {
		if r < utf8.RuneSelf {
			continue
		}
		if m := Domain(string(r)); m != string(r) && isASCII(m) && !punycode(m) {
			return true
		}
	}
	return false
}

// Context separates the durable sent-to signal from receipt-created contacts.
// KnownDomains contains own identity, sent-to and explicitly saved contact domains.
type Context struct {
	OwnAddresses    []string
	SentTo          bool
	ExplicitContact bool
	KnownDomains    []string
}

func mixedScript(host string) bool {
	// Common/Inherited punctuation and combining marks do not introduce a script.
	// Inspect labels separately: an ASCII TLD does not make a Cyrillic label mixed.
	for _, label := range strings.Split(decoded(host), ".") {
		scripts := map[string]bool{}
		for _, r := range label {
			if !unicode.IsLetter(r) {
				continue
			}
			for name, table := range unicode.Scripts {
				if name != "Common" && name != "Inherited" && unicode.Is(table, r) {
					scripts[name] = true
					break
				}
			}
		}
		if len(scripts) > 1 {
			return true
		}
	}
	return false
}

// Exempt lets callers skip parsing attacker-controlled bodies for outgoing or
// authenticated own-identity messages.
func Exempt(m model.DBMessage, c Context) bool {
	if m.FolderID == 2 || m.FolderID == 3 || m.FolderID == 5 {
		return true
	}
	from, err := mail.ParseAddress(m.FromAddr)
	if err != nil {
		return false
	}
	if senderauth.Decode(m.SenderAuthentication).Dmarc != "pass" {
		return false
	}
	for _, own := range c.OwnAddresses {
		if FoldAddress(own) == FoldAddress(from.Address) {
			return true
		}
	}
	return false
}

func Compute(m model.DBMessage, atts []api.AttachmentMeta, c Context) []api.MessageWarning {
	return ComputeWithLinks(m, atts, c, Links(m.BodyHTML))
}

// ComputeWithLinks reuses the parsed anchors needed for the repository's
// indexed correspondent-domain lookup.
func ComputeWithLinks(m model.DBMessage, atts []api.AttachmentMeta, c Context, links []Link) []api.MessageWarning {
	out := []api.MessageWarning{}
	if Exempt(m, c) {
		return out
	}
	auth := senderauth.Decode(m.SenderAuthentication)
	add := func(kind api.MessageWarningKind) *api.MessageWarning {
		out = append(out, api.MessageWarning{Kind: kind})
		return &out[len(out)-1]
	}
	from, err := mail.ParseAddress(m.FromAddr)
	if err != nil {
		// Missing, malformed or several mailboxes: the sender checks below
		// have no address to work with, so say so rather than staying silent.
		from = &mail.Address{}
		add(api.MessageWarningKindMalformedSender)
	} else if HasFormatChar(from.Address) {
		// Parses, but need not display as what it is. The checks below still
		// run on the address as received.
		add(api.MessageWarningKindMalformedSender)
	}
	if auth.Dmarc == "fail" {
		w := add(api.MessageWarningKindAuthFail)
		w.FailedChecks = []api.MessageWarningFailedChecksItem{"dmarc"}
		if !auth.DkimAligned {
			w.FailedChecks = append(w.FailedChecks, "dkim_alignment")
		}
		if auth.Spf == "fail" || auth.Spf == "softfail" {
			w.FailedChecks = append(w.FailedChecks, "spf")
		}
	}
	for _, token := range strings.FieldsFunc(from.Name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("@.-_+", r)
	}) {
		token = strings.Trim(token, ".")
		if strings.Contains(token, "@") {
			a, err := mail.ParseAddress(token)
			if err == nil && FoldAddress(a.Address) != FoldAddress(from.Address) {
				add(api.MessageWarningKindDisplayNameAddress).Address = api.NewOptString(token)
				break
			}
		} else if host := textHost(token); host != "" && Registrable(host) != Registrable(addressDomain(from.Address)) {
			add(api.MessageWarningKindDisplayNameAddress).Address = api.NewOptString(token)
			break
		}
	}
	// Build once: lookups for every link are constant-time in correspondent count.
	known := map[string]string{}
	exact := map[string]bool{}
	for _, d := range c.KnownDomains {
		d = Domain(d)
		for _, candidate := range []string{d, Registrable(d)} {
			exact[candidate] = true
			key := Skeleton(candidate)
			if prev, ok := known[key]; !ok || candidate < prev {
				known[key] = candidate
			}
		}
	}
	seen := map[string]bool{}
	// raw is the host as received. Whether it is an IDN is decided from that,
	// not from Domain(raw): compatibility characters map to plain ASCII.
	lookalike := func(raw string) {
		host := Domain(raw)
		compat := mapsToASCII(raw)
		key := host
		if compat {
			key = raw
		}
		if host == "" || net.ParseIP(host) != nil || seen[key] {
			return
		}
		seen[key] = true
		resembles := known[Skeleton(host)]
		if resembles == "" {
			resembles = known[Skeleton(Registrable(host))]
		}
		if !compat && (exact[host] || exact[Registrable(host)]) {
			resembles = ""
		}
		if !compat && !punycode(host) && !mixedScript(host) && resembles == "" {
			return
		}
		w := add(api.MessageWarningKindLookalikeDomain)
		if compat {
			// decoded(host) would display exactly as the domain imitated.
			w.Domain = api.NewOptString(raw)
		} else {
			w.Domain = api.NewOptString(decoded(host))
		}
		w.Punycode = api.NewOptString(host)
		if resembles != "" {
			w.Resembles = api.NewOptString(resembles)
		}
	}
	_, fromDomain, _ := strings.Cut(from.Address, "@")
	lookalike(fromDomain)
	if replies, err := mail.ParseAddressList(m.ReplyToAddr); err == nil {
		for _, reply := range replies {
			if Registrable(addressDomain(reply.Address)) != Registrable(addressDomain(from.Address)) {
				add(api.MessageWarningKindReplyToMismatch).Address = api.NewOptString(reply.Address)
			}
		}
	}
	if from.Address != "" && !c.SentTo && !c.ExplicitContact {
		add(api.MessageWarningKindFirstTimeSender)
	}
	for _, link := range links {
		host := LinkHost(link.Href)
		if host == "" {
			continue
		}
		lookalike(host)
		text := textHost(strings.TrimSpace(link.Text))
		if net.ParseIP(host) != nil || punycode(Domain(host)) || (text != "" && Registrable(text) != Registrable(host)) {
			w := add(api.MessageWarningKindDeceptiveLink)
			w.LinkIndex = api.NewOptInt(link.Index)
			w.Text = api.NewOptString(link.Text)
			w.Href = api.NewOptString(link.Href)
			w.DecodedHost = api.NewOptString(decoded(host))
		}
	}
	for _, a := range atts {
		if reason := AttachmentRisk(a.Filename, a.ContentType); reason != "" {
			w := add(api.MessageWarningKindRiskyAttachment)
			w.Filename = api.NewOptString(a.Filename)
			w.Reason = api.NewOptMessageWarningReason(api.MessageWarningReason(reason))
		}
	}
	return out
}

func LinkHost(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && (u.Scheme != "" || u.Host == "")) {
		return ""
	}
	return Domain(u.Hostname())
}
func textHost(s string) string {
	if strings.ContainsAny(s, " \t\r\n") {
		return ""
	}
	explicit := strings.Contains(s, "://")
	if !explicit {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return ""
	}
	h := LinkHost(s)
	if h == "" {
		return ""
	}
	if !explicit {
		// A dotted filename, version or decimal is not enough to claim URL text.
		// Private suffixes remain supported through their ICANN top-level domain.
		labels := strings.Split(h, ".")
		_, icann := publicsuffix.PublicSuffix(h)
		if !icann {
			_, icann = publicsuffix.PublicSuffix(labels[len(labels)-1])
		}
		if net.ParseIP(u.Hostname()) == nil && !icann {
			return ""
		}
	}
	if !strings.Contains(h, ".") && net.ParseIP(h) == nil {
		return ""
	}
	return h
}

// browserIP accepts the legacy IPv4 number syntax browsers navigate to as an
// IP, including a single integer, shortened dotted forms, hex and octal. This
// follows WHATWG IPv4 parsing; ordinary DNS names are left unchanged.
func browserIP(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return ""
	}
	values := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return ""
		}
		base := 10
		if strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X") {
			base = 16
			p = p[2:]
		} else if len(p) > 1 && p[0] == '0' {
			base = 8
			p = p[1:]
		}
		var v uint64
		var err error
		if p != "" {
			v, err = strconv.ParseUint(p, base, 32)
		}
		if err != nil {
			return ""
		}
		values[i] = v
		if i < len(parts)-1 && v > 255 {
			return ""
		}
	}
	last := values[len(values)-1]
	limit := uint64(1) << uint(8*(5-len(values)))
	if last >= limit {
		return ""
	}
	total := last
	for i, v := range values[:len(values)-1] {
		total += v << uint(8*(3-i))
	}
	return net.IPv4(byte(total>>24), byte(total>>16), byte(total>>8), byte(total)).String()
}

type Link struct {
	Index      int
	Text, Href string
}

// Links parses the document once, walks it iteratively, and collects each text
// node at most once. At most MaxLinks anchors are analysed. Indices count ALL
// anchors (including those without href), matching the confirmation-page API.
func Links(body string) []Link {
	// Bodies stored before ingest bounded formatting can still amplify.
	doc, err := html.Parse(strings.NewReader(sanitize.BoundFormatting(body)))
	if err != nil {
		// x/net/html bounds its open-element stack at 512. A deeply nested
		// message must not hide all its links merely by exceeding that bound.
		// Its tokenizer (also used by HasExternalImages) still decodes HTML and
		// entities linearly without constructing an unbounded element stack.
		return tokenizedLinks(body)
	}
	type visit struct {
		n    *html.Node
		exit bool
	}
	stack := []visit{{n: doc}}
	links := []Link{}
	index := 0
	active := -1
	var text strings.Builder
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := v.n
		if v.exit {
			if active >= 0 {
				links[active].Text = text.String()
				active = -1
				text.Reset()
			}
			continue
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			continue
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if index >= MaxLinks {
				break
			}
			href := ""
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
					break
				}
			}
			links = append(links, Link{Index: index, Href: href})
			index++
			active = len(links) - 1
			text.Reset()
			stack = append(stack, visit{n: n, exit: true})
		}
		if n.Type == html.TextNode && active >= 0 {
			text.WriteString(n.Data)
		}
		for ch := n.LastChild; ch != nil; ch = ch.PrevSibling {
			stack = append(stack, visit{n: ch})
		}
	}
	return links
}

var riskyExtensions = map[string]bool{}

func init() {
	for _, e := range strings.Fields("exe com bat cmd scr pif msi msp dll cpl ocx sys drv ps1 ps2 psm1 psd1 vbs vbe js jse wsf wsh hta sh bash zsh ksh fish py pyw rb pl php jar apk app dmg pkg deb rpm run desktop lnk reg vb vba iso img chm xll scpt scptd command url website gadget inf html htm xhtml shtml svg docm dotm xlsm xltm xlam pptm potm ppam ppsm sldm") {
		riskyExtensions["."+e] = true
	}
}
func AttachmentRisk(name, contentType string) string {
	name = attachmentname.Normalize(name)
	name = strings.ToLower(strings.TrimRight(strings.TrimSpace(name), ". "))
	ext := filepath.Ext(name)
	if prior := filepath.Ext(strings.TrimSuffix(name, ext)); ext != "" && prior != "" && prior != "." {
		return "double_extension"
	}
	if riskyExtensions[ext] {
		return "executable_extension"
	}
	ct, _, _ := mime.ParseMediaType(contentType)
	ct = strings.ToLower(ct)
	switch ct {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/vbscript", "application/x-httpd-php", "text/x-perl", "text/x-ruby", "text/x-shellscript", "application/x-iso9660-image", "application/vnd.ms-htmlhelp", "application/x-msdownload", "application/x-msdos-program", "application/vnd.microsoft.portable-executable", "application/x-executable", "application/x-sharedlib", "application/x-mach-binary", "application/x-dosexec", "application/x-sh", "application/x-shellscript", "application/x-bat", "application/x-powershell", "application/javascript", "text/javascript", "application/ecmascript", "text/ecmascript", "text/x-python", "application/x-python-code", "application/java-archive", "application/vnd.android.package-archive":
		return "executable_content_type"
	}
	return ""
}

func punycode(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if strings.HasPrefix(label, "xn--") {
			return true
		}
	}
	return false
}

// tokenizedLinks is the depth-limit fallback. Indices count anchor start tokens
// in source order; ordinary deeply nested markup retains document order.
func tokenizedLinks(body string) []Link {
	z := html.NewTokenizer(strings.NewReader(body))
	links := []Link{}
	active := -1
	var text strings.Builder
	finish := func() {
		if active >= 0 {
			links[active].Text = text.String()
			active = -1
			text.Reset()
		}
	}
	raw := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			finish()
			return links
		}
		if tt == html.TextToken {
			if active >= 0 && !raw {
				text.Write(z.Text())
			}
			continue
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken && tt != html.EndTagToken {
			continue
		}
		token := z.Token()
		if token.Data == "script" || token.Data == "style" {
			raw = tt != html.EndTagToken
			continue
		}
		if raw || token.Data != "a" {
			continue
		}
		finish()
		if tt == html.EndTagToken {
			if len(links) >= MaxLinks {
				return links
			}
			continue
		}
		if len(links) >= MaxLinks {
			return links
		}
		href := ""
		for _, a := range token.Attr {
			if a.Key == "href" {
				href = a.Val
				break
			}
		}
		links = append(links, Link{Index: len(links), Href: href})
		active = len(links) - 1
	}
}
