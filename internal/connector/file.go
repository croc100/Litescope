package connector

import "github.com/croc100/litescope/internal/schema"

type fileConnector struct {
	path string
}

func openFile(path string) (Connector, error) {
	return &fileConnector{path: path}, nil
}

func (f *fileConnector) Schema() (*schema.Schema, error) {
	return schema.Load(f.path)
}

func (f *fileConnector) Close() error { return nil }
func (f *fileConnector) DSN() string  { return f.path }
