package cli

import (
	"os"

	"github.com/croc100/litescope/internal/mcp"
	"github.com/spf13/cobra"
)

func cmdMCP() *cobra.Command {
	var allowWrites bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run Litescope as an MCP server for AI agents (Claude, etc.)",
		Long: `Start a Model Context Protocol server over stdio so an LLM agent — Claude
Desktop, Claude Code, or any MCP client — can call Litescope as a tool.

By default only read-only tools are exposed. Pass --allow-writes to also
enable litescope_rewind, litescope_d1_pull, litescope_query_write,
litescope_migrate_apply, litescope_d1_create, and litescope_d1_delete.

Add to your Claude Desktop config (claude_desktop_config.json):

  {
    "mcpServers": {
      "litescope": { "command": "litescope", "args": ["mcp"] }
    }
  }

For D1 with write access:

  {
    "mcpServers": {
      "litescope": {
        "command": "litescope",
        "args": ["mcp", "--allow-writes"],
        "env": {
          "CLOUDFLARE_API_TOKEN": "your-token",
          "CLOUDFLARE_ACCOUNT_ID": "your-account-id"
        }
      }
    }
  }

Then ask Claude things like "use litescope to check the health of ./app.db"
or "list my D1 databases and show me the schema of the users table".

Pass an optional database source (e.g. 'litescope mcp ./app.db') to bind it as
MCP resources — its schema and a data dictionary become readable to the agent
without spending a tool call. Schema/dictionary for any source are also always
available via resource templates.`,
		Args: cobra.MaximumNArgs(1),
		// The MCP server owns stdout for protocol messages — keep cobra quiet.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			version := cmd.Root().Version
			if version == "" {
				version = "dev"
			}
			var defaultSource string
			if len(args) == 1 {
				defaultSource = args[0]
			}
			return mcp.Serve(os.Stdin, os.Stdout, version, allowWrites, defaultSource)
		},
	}

	cmd.Flags().BoolVar(&allowWrites, "allow-writes", false,
		"Enable write tools: litescope_rewind, litescope_d1_pull, litescope_query_write, litescope_migrate_apply, litescope_d1_create, litescope_d1_delete")
	return cmd
}
