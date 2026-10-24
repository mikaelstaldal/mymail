package phishing

import (
	"fmt"
	"io"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
	"golang.org/x/net/html"
)

// RewriteLinks changes only served markup, disabling unchecked overflow anchors.
// The indices use the same document
// traversal as Links; no destination supplied by a caller enters the route.
func RewriteLinks(body string, id int64, warnings []api.MessageWarning) string {
	flagged := map[int]bool{}
	overflow := false
	for _, w := range warnings {
		if w.Kind == api.MessageWarningKindTooManyLinks {
			overflow = true
		}
		if w.Kind == api.MessageWarningKindDeceptiveLink && w.LinkIndex.Set {
			flagged[w.LinkIndex.Value] = true
		}
	}
	if len(flagged) == 0 && !overflow {
		return body
	}
	// Bounded exactly as in Links, so that anchor indices agree.
	doc, err := html.Parse(strings.NewReader(sanitize.BoundFormatting(body)))
	if err != nil {
		return rewriteTokenized(body, id, flagged, overflow)
	}
	stack := []*html.Node{doc}
	index := 0
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			continue
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if overflow && index >= MaxLinks {
				attrs := n.Attr[:0]
				for _, a := range n.Attr {
					if a.Key != "href" {
						attrs = append(attrs, a)
					}
				}
				n.Attr = attrs
			} else if flagged[index] {
				for i := range n.Attr {
					if n.Attr[i].Key == "href" {
						n.Attr[i].Val = fmt.Sprintf("../../../../#/link/%d/%d", id, index)
					}
				}
			}
			index++
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	var out strings.Builder
	// Return the body's fragment rather than nesting a second HTML document.
	var bodyNode *html.Node
	stack = []*html.Node{doc}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type == html.ElementNode && n.Data == "body" {
			bodyNode = n
			break
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	if bodyNode == nil {
		return body
	}
	for c := bodyNode.FirstChild; c != nil; c = c.NextSibling {
		if html.Render(&out, c) != nil {
			return body
		}
	}
	return out.String()
}

// Match Links' tokenizer fallback for documents beyond the parser stack limit.
func rewriteTokenized(body string, id int64, flagged map[int]bool, overflow bool) string {
	z := html.NewTokenizer(strings.NewReader(body))
	var out strings.Builder
	index := 0
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() == io.EOF {
				return out.String()
			}
			return body
		}
		raw := string(z.Raw())
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			token := z.Token()
			if token.Data == "a" {
				if overflow && index >= MaxLinks {
					attrs := token.Attr[:0]
					for _, a := range token.Attr {
						if a.Key != "href" {
							attrs = append(attrs, a)
						}
					}
					token.Attr = attrs
					raw = token.String()
				} else if flagged[index] {
					for i := range token.Attr {
						if token.Attr[i].Key == "href" {
							token.Attr[i].Val = fmt.Sprintf("../../../../#/link/%d/%d", id, index)
						}
					}
					raw = token.String()
				}
				index++
			}
		}
		out.WriteString(raw)
	}
}
