package ionet

import "fmt"

// ListContainers retrieves all containers for a specific deployment
func (c *Client) ListContainers(deploymentID string) (*ContainerList, error) {
	if deploymentID == "" {
		return nil, fmt.Errorf("deployment ID cannot be empty")
	}

	endpoint := fmt.Sprintf("/deployment/%s/containers", deploymentID)

	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var containerList ContainerList
	if err := decodeDataWithFlexibleTimes(resp.Body, &containerList); err != nil {
		return nil, fmt.Errorf("failed to parse containers list: %w", err)
	}

	return &containerList, nil
}

// GetContainerDetails retrieves detailed information about a specific container
func (c *Client) GetContainerDetails(deploymentID, containerID string) (*Container, error) {
	if deploymentID == "" {
		return nil, fmt.Errorf("deployment ID cannot be empty")
	}
	if containerID == "" {
		return nil, fmt.Errorf("container ID cannot be empty")
	}

	endpoint := fmt.Sprintf("/deployment/%s/container/%s", deploymentID, containerID)

	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get container details: %w", err)
	}

	// API response format not documented, assuming direct format
	var container Container
	if err := decodeWithFlexibleTimes(resp.Body, &container); err != nil {
		return nil, fmt.Errorf("failed to parse container details: %w", err)
	}

	return &container, nil
}

// buildLogEndpoint constructs the request path for fetching logs
func buildLogEndpoint(deploymentID, containerID string, opts *GetLogsOptions) (string, error) {
	if deploymentID == "" {
		return "", fmt.Errorf("deployment ID cannot be empty")
	}
	if containerID == "" {
		return "", fmt.Errorf("container ID cannot be empty")
	}

	params := make(map[string]interface{})

	if opts != nil {
		if opts.Level != "" {
			params["level"] = opts.Level
		}
		if opts.Stream != "" {
			params["stream"] = opts.Stream
		}
		if opts.Limit > 0 {
			params["limit"] = opts.Limit
		}
		if opts.Cursor != "" {
			params["cursor"] = opts.Cursor
		}
		if opts.Follow {
			params["follow"] = true
		}

		if opts.StartTime != nil {
			params["start_time"] = opts.StartTime
		}
		if opts.EndTime != nil {
			params["end_time"] = opts.EndTime
		}
	}

	endpoint := fmt.Sprintf("/deployment/%s/log/%s", deploymentID, containerID)
	endpoint += buildQueryParams(params)

	return endpoint, nil
}

// GetContainerLogsRaw retrieves the raw text logs for a specific container
func (c *Client) GetContainerLogsRaw(deploymentID, containerID string, opts *GetLogsOptions) (string, error) {
	endpoint, err := buildLogEndpoint(deploymentID, containerID, opts)
	if err != nil {
		return "", err
	}

	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get container logs: %w", err)
	}

	return string(resp.Body), nil
}
