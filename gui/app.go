package main

import (
	"context"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx context.Context
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// OpenFile opens a native file picker and returns the selected path.
func (a *App) OpenFile() string {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "SQLite databases", Pattern: "*.db;*.sqlite;*.sqlite3"},
		},
	})
	if err != nil {
		return ""
	}
	return path
}

// Diff compares two SQLite database files and returns the result.
func (a *App) Diff(oldPath, newPath string) (*diff.Result, error) {
	return diff.Compare(oldPath, newPath)
}

// Schema returns the schema of a single SQLite database file.
func (a *App) Schema(path string) (*schema.Schema, error) {
	return schema.Load(path)
}
