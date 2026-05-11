package ai

import (
	"github.com/google/generative-ai-go/genai"
)

// GetLocalTools returns the list of local actions as genai.FunctionDeclaration
func GetLocalTools() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "RestartContainer",
			Description: "Restarts a specific Docker container by its ID.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The ID or name of the Docker container to restart.",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "StopContainer",
			Description: "Stops a running Docker container.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The ID of the container to stop.",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "GetContainerLogs",
			Description: "Fetches the last 10 lines of logs for a container.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The ID of the container.",
					},
				},
				Required: []string{"containerID"},
			},
		},
	}
}
