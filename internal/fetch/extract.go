package fetch

import (
	"bytes"
	"golang.org/x/net/html"
	"strings"
)

func extract(body []byte, media string) (string, string) {
	if media == "text/plain" {
		return "", strings.TrimSpace(string(body))
	}
	doc, e := html.Parse(bytes.NewReader(body))
	if e != nil {
		return "", strings.TrimSpace(string(body))
	}
	title := ""
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" {
			title = strings.TrimSpace(nodeText(n))
			return
		}
		for c := n.FirstChild; c != nil && title == ""; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	var out strings.Builder
	fence := strings.Repeat(string(rune(96)), 3)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "iframe", "canvas", "svg", "form", "input", "button", "nav", "header", "footer", "head", "title", "meta", "link":
				return
			}
		}
		if n.Type == html.TextNode {
			v := strings.Join(strings.Fields(n.Data), " ")
			if v != "" {
				out.WriteString(v)
				out.WriteByte(' ')
			}
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				out.WriteString("\n\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " ")
			case "p", "div", "section", "article", "li", "tr", "blockquote":
				out.WriteByte('\n')
			case "pre":
				out.WriteString("\n" + fence + "\n")
			case "code":
				out.WriteByte(byte(96))
			case "br":
				out.WriteByte('\n')
			case "img":
				for _, a := range n.Attr {
					if a.Key == "alt" && a.Val != "" {
						out.WriteString(a.Val + " ")
					}
				}
			case "td", "th":
				out.WriteString(" | ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "section", "article", "li", "tr", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6":
				out.WriteByte('\n')
			case "pre":
				out.WriteString("\n" + fence + "\n")
			case "code":
				out.WriteByte(byte(96))
			}
		}
	}
	walk(doc)
	return title, strings.TrimSpace(out.String())
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
