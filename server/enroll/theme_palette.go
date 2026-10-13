package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var themePaletteFile = envOr("ENROLL_THEME_PALETTE_FILE", "/var/lib/fleet-enroll/theme-palette.json")
var themePaletteMu sync.Mutex
var paletteColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type paletteColors struct {
	Canvas    string `json:"canvas"`
	Accent    string `json:"accent"`
	Highlight string `json:"highlight"`
	Text      string `json:"text"`
}
type themePalette struct {
	Light paletteColors `json:"light"`
	Dark  paletteColors `json:"dark"`
}

func (p *themePalette) validate() bool {
	for _, colors := range []*paletteColors{&p.Light, &p.Dark} {
		for _, color := range []*string{&colors.Canvas, &colors.Accent, &colors.Highlight, &colors.Text} {
			if !paletteColorRe.MatchString(*color) {
				return false
			}
			*color = strings.ToUpper(*color)
		}
	}
	return true
}
func readThemePaletteLocked() (*themePalette, error) {
	raw, err := os.ReadFile(themePaletteFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var palette themePalette
	if err := json.Unmarshal(raw, &palette); err != nil {
		return nil, err
	}
	if !palette.validate() {
		return nil, fmt.Errorf("invalid stored palette")
	}
	return &palette, nil
}
func loadThemePalette() (*themePalette, error) {
	themePaletteMu.Lock()
	defer themePaletteMu.Unlock()
	return readThemePaletteLocked()
}
func saveThemePaletteLocked(palette *themePalette) error {
	raw, err := json.Marshal(palette)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(themePaletteFile), ".theme-palette-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), themePaletteFile)
}
func handleThemePalette(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Palette  *themePalette `json:"palette"`
		IfAbsent bool          `json:"ifAbsent"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil || req.Palette == nil || !req.Palette.validate() {
		writeErr(w, 400, "配色须包含浅色和深色的八个 #RRGGBB 颜色")
		return
	}
	themePaletteMu.Lock()
	defer themePaletteMu.Unlock()
	current, err := readThemePaletteLocked()
	if err == nil && (current == nil || !req.IfAbsent) {
		err = saveThemePaletteLocked(req.Palette)
		if err == nil {
			current = req.Palette
		}
	}
	if err != nil {
		log.Printf("保存配色失败: %v", err)
		writeErr(w, 500, "配色保存失败")
		return
	}
	writeJSON(w, 200, map[string]interface{}{"palette": current})
}
