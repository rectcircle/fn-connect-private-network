package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

func TestTranslationCatalogCoverage(t *testing.T) {
	catalogs := map[string]map[string]string{}
	for _, locale := range []string{"en", "zh-Hans"} {
		data, err := translationsFS.ReadFile("translations/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var table map[string]string
		if err = json.Unmarshal(data, &table); err != nil {
			t.Fatal(err)
		}
		catalogs[locale] = table
	}
	check := func(key string) {
		for locale, table := range catalogs {
			if strings.TrimSpace(table[key]) == "" {
				t.Errorf("missing %s translation: %s", locale, key)
			}
		}
	}
	files, _ := filepath.Glob("*.go")
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "tr" || len(call.Args) == 0 {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				key, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				check(key)
			}
			return true
		})
		if file != "prewarm.go" {
			wrapped := map[ast.Node]bool{}
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && len(c.Args) > 0 {
					if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "tr" {
						wrapped[c.Args[0]] = true
					}
				}
				return true
			})
			ast.Inspect(f, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && !wrapped[n] {
					text, _ := strconv.Unquote(lit.Value)
					for _, r := range text {
						if unicode.Is(unicode.Han, r) {
							t.Errorf("unlocalized UI text in %s: %s", file, text)
							break
						}
					}
				}
				return true
			})
		}
	}
	native, err := os.ReadFile("internal/iosui/ui_ios.m")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range regexp.MustCompile(`uiText\("([^"\n]+)"\)`).FindAllStringSubmatch(string(native), -1) {
		check(match[1])
	}
	for key, text := range catalogs["en"] {
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("untranslated English text %q: %q", key, text)
				break
			}
		}
	}
}

func TestTranslationLanguageResolution(t *testing.T) {
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	// Fyne registers its built-in Chinese controls under this script-specific tag.
	// Loading only the application catalogs misses the generic-zh collision.
	if _, err := bundle.ParseMessageFileBytes([]byte(`{"Cancel":"取消"}`), "base.zh_Hans.json"); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-Hans"} {
		data, _ := translationsFS.ReadFile("translations/" + locale + ".json")
		if _, err := bundle.ParseMessageFileBytes(data, locale+".json"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ locale, key, want string }{{"en-US", "打开日志", "Open logs"}, {"zh-CN", "打开日志", "打开日志"}, {"zh", "打开日志", "打开日志"}, {"en-GB", "已断开连接", "Disconnected"}, {"zh-Hans", "FnCPN 日志快照", "FnCPN 日志快照"}, {"fr-FR", "导出诊断与日志", "Export diagnostics and logs"}} {
		got, err := i18n.NewLocalizer(bundle, tc.locale).Localize(&i18n.LocalizeConfig{MessageID: tc.key})
		if err != nil || got != tc.want {
			t.Errorf("%s %q: got %q, %v", tc.locale, tc.key, got, err)
		}
	}
}
