package mcp

import (
	"context"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type MCPManager struct {
	mcpClient *client.Client
}

// NewMCPManager initializes the MCP connection by spawning the npx command
func NewMCPManager() (*MCPManager, error) {
	// We use 'npx -y mcp-server-kubernetes' to run the server in the background
	mcpClient, err := client.NewStdioMCPClient("npx", nil, "-y", "mcp-server-kubernetes")
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %v", err)
	}

	ctx := context.Background()
	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "gopher-ops-client",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MCP client: %v", err)
	}

	log.Println("✅ Connected to MCP Kubernetes Server successfully!")
	return &MCPManager{mcpClient: mcpClient}, nil
}

// ListTools returns the available tools from the MCP server
func (m *MCPManager) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	resp, err := m.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Tools, nil
}

// CallTool executes a specific tool with arguments
func (m *MCPManager) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	resp, err := m.mcpClient.CallTool(ctx, req)
	if err != nil {
		return "", err
	}

	// MCP responses can be complex, for now we just return the text content
	var result string
	for _, content := range resp.Content {
		// Use type switch to handle different content types
		switch c := content.(type) {
		case mcp.TextContent:
			result += c.Text
		case *mcp.TextContent:
			result += c.Text
		}
	}

	return result, nil
}

// Close shuts down the MCP server connection
func (m *MCPManager) Close() {
	if m.mcpClient != nil {
		m.mcpClient.Close()
	}
}
