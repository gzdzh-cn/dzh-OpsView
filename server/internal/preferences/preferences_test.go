package preferences

import (
	"opsview/internal/model"
	"testing"
)

func TestControlledAppearanceAndValues(t *testing.T) {
	for _, v := range []model.Config{{"theme": "<script>"}, {"fontSize": 14.5}, {"fontSize": "14"}, {"hiddenMenus": []string{"/settings"}}, {"favicon": "data:image/png;base64,AAAA"}, {"primaryColor": "red;display:none"}, {"hiddenMenus": []any{"/unknown"}}, {"customCSS": "body{}"}} {
		if Validate("appearance", v) == nil {
			t.Fatal("unsafe setting", v)
		}
	}
	if err := Validate("appearance", model.Config{"theme": "dark", "fontSize": 14, "hiddenMenus": []string{"/sites"}, "primaryColor": "#123abc"}); err != nil {
		t.Fatal(err)
	}
	if Validate("security", model.Config{"loginCountries": []string{"notacountry"}}) == nil {
		t.Fatal("invalid country")
	}
}
