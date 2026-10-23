package server

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestSubPageRendersQRCode(t *testing.T) {
	data := subPageData{
		Title:       "连接信息",
		PageNote:    "你的个人订阅",
		PanelName:   "MikvAce Panel",
		Username:    "demo",
		UsedLabel:   "1.0 GB",
		LimitLabel:  "10.0 GB",
		UsedPercent: 10,
		ExpireLabel: "不限制",
		SubURL:      "http://panel.example.com/api/files/token",
		ClashURL:    "http://panel.example.com/api/files/token?format=clash",
		SingboxURL:  "http://panel.example.com/api/files/token?format=singbox",
		URIURL:      "http://panel.example.com/api/files/token?format=uri",
		QRData:      template.URL("data:image/png;base64,AAAA"),
		Year:        2026,
	}
	var out bytes.Buffer
	if err := subPageTemplate.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	if strings.Contains(page, "ZgotmplZ") {
		t.Fatal("QR data URI was filtered by html/template")
	}
	if !strings.Contains(page, `src="data:image/png;base64,AAAA"`) {
		t.Fatalf("QR data URI missing from page")
	}
	if !strings.Contains(page, "流量进度") || !strings.Contains(page, "10%") {
		t.Fatalf("usage progress missing from page")
	}
}

func TestSubPageErrorRendersContext(t *testing.T) {
	data := subPageData{
		Title:      "订阅不可用",
		PanelName:  "MikvAce Panel",
		SupportURL: "https://example.com/support",
		Error:      "Expired",
		Year:       2026,
	}
	var out bytes.Buffer
	if err := subPageErrorTemplate.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{"Expired", "MikvAce Panel", "https://example.com/support"} {
		if !strings.Contains(page, want) {
			t.Fatalf("error page missing %q", want)
		}
	}
}
