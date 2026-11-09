package htmlutil

import (
	"strings"

	"golang.org/x/net/html"
)

func Parse(r interface{ Read([]byte) (int, error) }) (*html.Node, error) {
	return html.Parse(r)
}

func ParseString(s string) (*html.Node, error) {
	return html.Parse(strings.NewReader(s))
}

func Attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func HasClass(n *html.Node, class string) bool {
	if n == nil {
		return false
	}
	for _, c := range strings.Fields(Attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func Text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func TextTrim(n *html.Node) string {
	return strings.TrimSpace(Text(n))
}

func Find(n *html.Node, pred func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if pred(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := Find(c, pred); found != nil {
			return found
		}
	}
	return nil
}

func FindAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if pred(node) {
			out = append(out, node)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	if n != nil {
		walk(n)
	}
	return out
}

func Tag(name string, class ...string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != name {
			return false
		}
		for _, c := range class {
			if !HasClass(n, c) {
				return false
			}
		}
		return true
	}
}

func Element(name string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == name
	}
}

func AttrEquals(key, value string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		return Attr(n, key) == value
	}
}

// DirectChildren returns only element children.
func DirectChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// NextSiblingElement returns the next element sibling.
func NextSiblingElement(n *html.Node) *html.Node {
	for c := n.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

// Parent returns the parent node.
func Parent(n *html.Node) *html.Node {
	return n.Parent
}
