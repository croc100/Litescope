package cli

import (
	"os"

	"github.com/croc100/litescope/internal/mcp"
	"github.com/spf13/cobra"
)

func cmdMCP() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run Litescope as an MCP server for AI agents (Claude, etc.)",
		Long: `Start a Model Context Protocol server over stdio so an LLM agent — Claude
Desktop, Claude Code, or any MCP client — can call Litescope as a tool.

This exposes read-only diagnostic tools (health, schema, diff) that let an AI
inspect your SQLite databases safely; it never mutates them.

Add to your Claude Desktop config (claude_desktop_config.json):

  {
    "mcpServers": {
      "litescope": { "command": "litescope", "args": ["mcp"] }
    }
  }

Then ask Claude things like "use litescope to check the health of ./app.db".`,
		Args: cobra.NoArgs,
		// The MCP server owns stdout for protocol messages — keep cobra quiet.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			version := cmd.Root().Version
			if version == "" {
				version = "dev"
			}
			return mcp.Serve(os.Stdin, os.Stdout, version)
		},
	}
}
