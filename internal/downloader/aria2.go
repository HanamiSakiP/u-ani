package downloader

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

type Aria2 struct {
	rpcURL string
	token  string
}

func newAria2() *Aria2 {
	token := config.Cfg.Aria2.RPCToken
	if !strings.HasPrefix(token, "token:") {
		token = "token:" + token
	}
	return &Aria2{rpcURL: config.Cfg.Aria2.RPCURL, token: token}
}

func (a *Aria2) CheckConfig() {
	if !strings.HasSuffix(config.Cfg.Aria2.RPCURL, "/rpc") {
		logger.Warning("make sure you are using xml-rpc endpoint of aria2")
	}
	if !strings.HasPrefix(config.Cfg.Aria2.RPCToken, "token:") {
		logger.Warning("rpc token should starts with `token:`")
	}
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func xmlValue(v any) string {
	switch t := v.(type) {
	case string:
		return "<value><string>" + xmlEscape(t) + "</string></value>"
	case int:
		return fmt.Sprintf("<value><int>%d</int></value>", t)
	case int64:
		return fmt.Sprintf("<value><int>%d</int></value>", t)
	case bool:
		if t {
			return "<value><boolean>1</boolean></value>"
		}
		return "<value><boolean>0</boolean></value>"
	case []string:
		items := make([]string, len(t))
		for i, s := range t {
			items[i] = xmlValue(s)
		}
		return "<value><array><data>" + strings.Join(items, "") + "</data></array></value>"
	case []any:
		items := make([]string, len(t))
		for i, s := range t {
			items[i] = xmlValue(s)
		}
		return "<value><array><data>" + strings.Join(items, "") + "</data></array></value>"
	case map[string]string:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("<value><struct>")
		for _, k := range keys {
			b.WriteString("<member><name>" + xmlEscape(k) + "</name>" + xmlValue(t[k]) + "</member>")
		}
		b.WriteString("</struct></value>")
		return b.String()
	default:
		return "<value><string>" + xmlEscape(fmt.Sprint(v)) + "</string></value>"
	}
}

func (a *Aria2) call(method string, params ...any) (any, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	b.WriteString(xmlEscape(method))
	b.WriteString(`</methodName><params>`)
	for _, p := range params {
		b.WriteString("<param>")
		b.WriteString(xmlValue(p))
		b.WriteString("</param>")
	}
	b.WriteString(`</params></methodCall>`)

	req, err := http.NewRequest("POST", a.rpcURL, bytes.NewReader([]byte(b.String())))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("aria2 rpc HTTP %d", resp.StatusCode)
	}

	var parsed xmlrpcResponse
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Fault != nil && len(parsed.Fault.Value.Struct) > 0 {
		msg := "aria2 rpc fault"
		for _, m := range parsed.Fault.Value.Struct {
			if m.Name == "faultString" {
				msg = m.Value.text()
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}
	if len(parsed.Params) == 0 {
		return nil, nil
	}
	return parsed.Params[0].Value.native(), nil
}

type xmlrpcResponse struct {
	Params []struct {
		Value xmlValueNode `xml:"value"`
	} `xml:"params>param"`
	Fault *struct {
		Value xmlValueNode `xml:"value"`
	} `xml:"fault"`
}

type xmlValueNode struct {
	String string          `xml:"string"`
	Int    int             `xml:"int"`
	Array  *xmlArrayNode   `xml:"array"`
	Struct []xmlStructNode `xml:"struct>member"`
	Raw    string          `xml:",chardata"`
}

type xmlArrayNode struct {
	Values []xmlValueNode `xml:"data>value"`
}

type xmlStructNode struct {
	Name  string       `xml:"name"`
	Value xmlValueNode `xml:"value"`
}

func (v xmlValueNode) text() string {
	if v.String != "" {
		return v.String
	}
	return strings.TrimSpace(v.Raw)
}

func (v xmlValueNode) native() any {
	switch {
	case v.Array != nil:
		out := make([]any, 0, len(v.Array.Values))
		for _, item := range v.Array.Values {
			out = append(out, item.native())
		}
		return out
	case len(v.Struct) > 0:
		out := map[string]any{}
		for _, m := range v.Struct {
			out[m.Name] = m.Value.native()
		}
		return out
	case v.Int != 0 || v.text() != "":
		if v.Int != 0 {
			return v.Int
		}
		return v.text()
	default:
		return ""
	}
}

func (a *Aria2) AddDownload(rawURL, savePath string) (string, error) {
	a.CheckConfig()
	result, err := a.call("aria2.addUri", a.token, []any{rawURL}, map[string]string{"dir": savePath})
	if err != nil {
		return "", err
	}
	return fmt.Sprint(result), nil
}

func (a *Aria2) GetStatus(id string) (Status, error) {
	result, err := a.call("aria2.tellStatus", a.token, id, []string{"status"})
	if err != nil {
		return Error, err
	}
	m, _ := result.(map[string]any)
	status, _ := m["status"].(string)
	switch status {
	case "active", "waiting":
		return Downloading, nil
	case "paused":
		return NotDownloading, nil
	case "error":
		return Error, nil
	case "complete":
		return Done, nil
	default:
		return Error, nil
	}
}

func (a *Aria2) GetFiles(id string) ([]string, error) {
	result, err := a.call("aria2.getFiles", a.token, id)
	if err != nil {
		return nil, err
	}
	items, _ := result.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if p, _ := m["path"].(string); p != "" {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func (a *Aria2) RemoveDownload(id string) error {
	_, err := a.call("aria2.removeDownloadResult", a.token, id)
	return err
}
