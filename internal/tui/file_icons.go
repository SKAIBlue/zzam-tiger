package tui

import (
	"path/filepath"
	"strings"
)

const (
	fileIconDefault   = ""
	directoryIcon     = ""
	directoryOpenIcon = ""
)

// workspaceFileIcon returns a Nerd Font icon based on well-known filenames
// first, then on the file extension. Unknown files intentionally keep a
// generic file icon so every entry has the same visual alignment.
func workspaceFileIcon(name string) string {
	base := strings.ToLower(filepath.Base(name))
	switch base {
	case "dockerfile", "containerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
		return ""
	case "makefile", "gnumakefile", "cmakelists.txt":
		return ""
	case "license", "license.md", "license.txt", "copying":
		return ""
	case ".gitignore", ".gitattributes", ".gitmodules":
		return ""
	case "go.mod", "go.sum", "go.work":
		return ""
	case "package.json", "package-lock.json", "tsconfig.json":
		return ""
	}

	switch strings.ToLower(filepath.Ext(base)) {
	case ".go":
		return ""
	case ".js", ".mjs", ".cjs":
		return ""
	case ".ts":
		return ""
	case ".jsx", ".tsx":
		return ""
	case ".json", ".jsonc":
		return ""
	case ".yaml", ".yml", ".toml", ".ini", ".conf", ".config", ".env":
		return ""
	case ".md", ".markdown", ".mdx":
		return ""
	case ".html", ".htm":
		return ""
	case ".css":
		return ""
	case ".scss", ".sass", ".less":
		return ""
	case ".py", ".pyi":
		return ""
	case ".rs":
		return ""
	case ".java", ".jar":
		return ""
	case ".c", ".h":
		return ""
	case ".cc", ".cpp", ".cxx", ".hpp":
		return ""
	case ".sh", ".bash", ".zsh", ".fish":
		return ""
	case ".sql":
		return ""
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp":
		return ""
	case ".zip", ".gz", ".tgz", ".bz2", ".xz", ".tar", ".7z", ".rar":
		return ""
	case ".pdf":
		return ""
	case ".lock":
		return ""
	}
	return fileIconDefault
}
