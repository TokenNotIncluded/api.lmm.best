package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const openSourceBountyMCPProtocolVersion = "2026-07-28"

type bountyMCPOutput struct {
	Message        string `json:"message"`
	Data           any    `json:"data,omitempty"`
	RemainingQuota int    `json:"remaining_quota,omitempty"`
}

type bountyMCPListInput struct {
	Page     int `json:"page,omitempty" jsonschema:"Page number, starting at 1"`
	PageSize int `json:"page_size,omitempty" jsonschema:"Items per page, from 1 to 50"`
}

type bountyMCPProjectInput struct {
	ProjectId int `json:"project_id" jsonschema:"Open-source bounty project identifier"`
}

type bountyMCPChallengeInput struct {
	ChallengeId int `json:"challenge_id" jsonschema:"Open-source bounty challenge identifier"`
}

type bountyMCPDraftInput struct {
	RepositoryUrl string `json:"repository_url" jsonschema:"Public GitHub repository URL"`
	Title         string `json:"title" jsonschema:"Bounty title"`
	Description   string `json:"description" jsonschema:"Eligible real-defect scope and impact"`
	Rules         string `json:"rules" jsonschema:"Verification, test, quality, and exclusion rules"`
	RewardQuota   int    `json:"reward_quota" jsonschema:"Gross listed price for each approved fix before the public platform fee"`
	RewardSlots   int    `json:"reward_slots" jsonschema:"Number of funded contributor slots"`
}

type bountyMCPUpdateDraftInput struct {
	ProjectId int `json:"project_id" jsonschema:"Draft bounty project identifier"`
	bountyMCPDraftInput
}

type bountyMCPAcceptInput struct {
	ProjectId    int    `json:"project_id" jsonschema:"Published bounty project identifier"`
	GithubHandle string `json:"github_handle" jsonschema:"The authenticated user's GitHub handle"`
}

type bountyMCPSubmitInput struct {
	ProjectId      int    `json:"project_id" jsonschema:"Accepted bounty project identifier"`
	IssueUrl       string `json:"issue_url,omitempty" jsonschema:"Optional GitHub Issue URL; provide this, pull_request_url, or both"`
	PullRequestUrl string `json:"pull_request_url,omitempty" jsonschema:"Optional focused GitHub pull request URL; provide this, issue_url, or both"`
	SubmissionNote string `json:"submission_note,omitempty" jsonschema:"Optional completion note for direct publisher review"`
}

type bountyMCPReviewInput struct {
	ChallengeId   int    `json:"challenge_id" jsonschema:"Submitted challenge identifier"`
	ReviewNote    string `json:"review_note,omitempty" jsonschema:"Detailed acceptance or rejection note"`
	RatingScore   int    `json:"rating_score" jsonschema:"Public contributor rating from 1 to 5"`
	RatingComment string `json:"rating_comment" jsonschema:"Public contributor evaluation"`
}

type bountyMCPTipInput struct {
	ChallengeId int    `json:"challenge_id" jsonschema:"Challenge whose contributor receives the tip"`
	Quota       int    `json:"quota" jsonschema:"Positive tip transferred from the publisher's own balance"`
	Note        string `json:"note,omitempty" jsonschema:"Public encouragement or partial-work note"`
}

type bountyMCPRateOwnerInput struct {
	ChallengeId int    `json:"challenge_id" jsonschema:"Reviewed challenge identifier"`
	Score       int    `json:"score" jsonschema:"Public publisher/verifier rating from 1 to 5"`
	Comment     string `json:"comment" jsonschema:"Public publisher/verifier evaluation"`
}

type bountyMCPDisputeListInput struct {
	Status string `json:"status,omitempty" jsonschema:"Optional status: open, resolved_paid, or resolved_denied"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum cases returned, default 50 and capped at 100"`
}

type bountyMCPOpenDisputeInput struct {
	ChallengeId int    `json:"challenge_id" jsonschema:"Challenge identifier"`
	Reason      string `json:"reason" jsonschema:"One of merged_but_unpaid, requirements_met_but_rejected, misleading_requirements, abusive_conduct, or other"`
	Statement   string `json:"statement" jsonschema:"Detailed factual dispute statement for third-party review"`
}

type bountyMCPResolveDisputeInput struct {
	DisputeId  int    `json:"dispute_id" jsonschema:"Dispute identifier"`
	Action     string `json:"action" jsonschema:"Resolution action: pay or deny"`
	Resolution string `json:"resolution" jsonschema:"Public administrator resolution explaining the evidence and decision"`
}

func bountyMCPBool(value bool) *bool { return &value }

func bountyMCPTool(name string, title string, description string, readOnly bool, destructive bool, idempotent bool) *mcp.Tool {
	return &mcp.Tool{
		Name: name, Title: title, Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title: title, ReadOnlyHint: readOnly, DestructiveHint: bountyMCPBool(destructive),
			IdempotentHint: idempotent, OpenWorldHint: bountyMCPBool(false),
		},
	}
}

func bountyMCPUserId(request *mcp.CallToolRequest) (int, error) {
	if request == nil || request.Extra == nil || request.Extra.TokenInfo == nil {
		return 0, fmt.Errorf("MCP authentication context is missing")
	}
	userId, err := strconv.Atoi(request.Extra.TokenInfo.UserID)
	if err != nil || userId <= 0 {
		return 0, fmt.Errorf("MCP authentication context is invalid")
	}
	return userId, nil
}

func bountyMCPError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", model.OpenSourceBountyErrorCode(err), err)
}

func bountyMCPRemainingQuota(userId int) int {
	var quota int
	if err := model.DB.Model(&model.User{}).Where("id = ?", userId).Select("quota").Scan(&quota).Error; err != nil {
		return 0
	}
	return quota
}

func bountyMCPIsAdmin(userId int) bool {
	var role int
	return model.DB.Model(&model.User{}).Where("id = ?", userId).Select("role").Scan(&role).Error == nil && role >= common.RoleAdminUser
}

func bountyMCPConfirmedOperation(request *mcp.CallToolRequest, userId int, toolName string, input any, message string) (*mcp.CallToolResult, *model.OpenSourceBountyMCPConfirmedOperation, error) {
	payloadHash, err := model.OpenSourceBountyMCPPayloadHash(input)
	if err != nil {
		return nil, nil, err
	}
	if request.Params.RequestState == "" {
		state, err := model.CreateOpenSourceBountyMCPConfirmation(userId, toolName, payloadHash)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{
				"confirmation": &mcp.ElicitParams{
					Mode: "form", Message: message,
					RequestedSchema: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"confirmed": {Type: "boolean", Description: "Set to true only after the user explicitly confirms this exact action."},
						},
						Required: []string{"confirmed"},
					},
				},
			},
			RequestState: state,
		}, nil, nil
	}
	response, ok := request.Params.InputResponses["confirmation"].(*mcp.ElicitResult)
	if !ok || response == nil {
		return nil, nil, fmt.Errorf("explicit user confirmation is required")
	}
	confirmed, _ := response.Content["confirmed"].(bool)
	if response.Action != "accept" || !confirmed {
		return nil, nil, fmt.Errorf("the user declined or cancelled this action")
	}
	return nil, &model.OpenSourceBountyMCPConfirmedOperation{
		State: request.Params.RequestState, ToolName: toolName, PayloadHash: payloadHash,
	}, nil
}

func bountyMCPDraft(input bountyMCPDraftInput) model.OpenSourceBountyDraftInput {
	return model.OpenSourceBountyDraftInput{
		RepositoryUrl: input.RepositoryUrl, Title: input.Title, Description: input.Description,
		Rules:       input.Rules,
		RewardQuota: input.RewardQuota, RewardSlots: input.RewardSlots,
	}
}

func registerOpenSourceBountyMCPTools(server *mcp.Server) {
	mcp.AddTool(server, bountyMCPTool("open_source_bounties.accept", "Accept an open-source bounty", "Reserve one funded slot for the authenticated user and record their GitHub handle.", false, false, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPAcceptInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			challenge, err := model.AcceptOpenSourceBounty(userId, input.ProjectId, input.GithubHandle)
			return nil, bountyMCPOutput{Message: "Bounty accepted.", Data: challenge}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.approve", "Approve, rate, and pay a submission", "Approve a genuine fix, publish a 1-5 contributor rating, and transfer the escrowed reward. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPReviewInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.approve", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				challengeId, _ := replay["challenge_id"].(float64)
				var challenge model.OpenSourceBountyChallenge
				err := model.DB.First(&challenge, int(challengeId)).Error
				return nil, bountyMCPOutput{Message: "Submission approval and payment already completed.", Data: map[string]any{"challenge": &challenge, "transferred_quota": replay["transferred_quota"]}}, bountyMCPError(err)
			}
			var snapshot struct {
				model.OpenSourceBountyChallenge
				ProjectTitle     string `json:"project_title"`
				EscrowQuota      int    `json:"escrow_quota"`
				ProjectUpdatedAt int64  `json:"project_updated_at"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").Joins("JOIN open_source_bounty_projects project ON project.id = challenge.project_id AND project.owner_user_id = ?", userId).Where("challenge.id = ?", input.ChallengeId).Select("challenge.*, project.title AS project_title, project.escrow_quota, project.updated_at AS project_updated_at").Scan(&snapshot).Error; err != nil || snapshot.Id == 0 {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			message := fmt.Sprintf("Approve challenge %d for %q, publicly rate the contributor %d/5, and transfer %d quota from the current %d escrow?", snapshot.Id, snapshot.ProjectTitle, input.RatingScore, snapshot.RewardQuota, snapshot.EscrowQuota)
			confirmationPayload := map[string]any{"input": input, "snapshot": snapshot}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.approve", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			updated, transferred, err := model.ReviewOpenSourceBountyChallengeWithMCPConfirmation(userId, input.ChallengeId, true, input.ReviewNote, input.RatingScore, input.RatingComment, *operation)
			return nil, bountyMCPOutput{Message: "Submission approved, rated, and paid.", Data: map[string]any{"challenge": updated, "transferred_quota": transferred}}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.close", "Close and refund a bounty", "Close a published or paused bounty and refund only unused escrow. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPProjectInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.close", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				projectId, _ := replay["project_id"].(float64)
				project, err := model.GetOpenSourceBountyProject(int(projectId))
				return nil, bountyMCPOutput{Message: "Bounty closure and escrow refund already completed.", Data: map[string]any{"project": project, "refunded_quota": replay["refunded_quota"]}, RemainingQuota: bountyMCPRemainingQuota(userId)}, bountyMCPError(err)
			}
			project, err := model.GetOpenSourceBountyProject(input.ProjectId)
			if err != nil || project.OwnerUserId != userId {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "bounty is unavailable"})
			}
			message := fmt.Sprintf("Close bounty %q and refund its %d unused escrow quota to your balance?", project.Title, project.EscrowQuota)
			confirmationPayload := map[string]any{"input": input, "project": project, "remaining_quota": bountyMCPRemainingQuota(userId)}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.close", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			updated, refunded, err := model.CloseOpenSourceBountyWithMCPConfirmation(userId, input.ProjectId, *operation)
			return nil, bountyMCPOutput{Message: "Bounty closed and unused escrow refunded.", Data: map[string]any{"project": updated, "refunded_quota": refunded}, RemainingQuota: bountyMCPRemainingQuota(userId)}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.cancel", "Cancel an unsubmitted challenge", "Cancel a publisher-owned challenge that has no submitted work and release its reward slot. Requires explicit user confirmation.", false, true, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPChallengeInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.cancel", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				challengeId, _ := replay["challenge_id"].(float64)
				var challenge model.OpenSourceBountyChallenge
				err := model.DB.First(&challenge, int(challengeId)).Error
				return nil, bountyMCPOutput{Message: "Challenge cancellation already completed.", Data: &challenge}, bountyMCPError(err)
			}
			var snapshot struct {
				model.OpenSourceBountyChallenge
				ProjectTitle string `json:"project_title"`
				EscrowQuota  int    `json:"escrow_quota"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").
				Joins("JOIN open_source_bounty_projects project ON project.id = challenge.project_id AND project.owner_user_id = ?", userId).
				Where("challenge.id = ?", input.ChallengeId).
				Select("challenge.*, project.title AS project_title, project.escrow_quota").
				Scan(&snapshot).Error; err != nil || snapshot.Id == 0 {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			message := fmt.Sprintf("Cancel the unsubmitted challenge %d from %q? This releases its reserved reward slot; no balance is refunded until the bounty is closed.", snapshot.Id, snapshot.ProjectTitle)
			confirmationPayload := map[string]any{"input": input, "snapshot": snapshot}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.cancel", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			challenge, err := model.CancelOpenSourceBountyChallengeWithMCPConfirmation(userId, input.ChallengeId, *operation)
			return nil, bountyMCPOutput{Message: "Unsubmitted challenge cancelled and its reward slot released.", Data: challenge}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.create_draft", "Create a bounty draft", "Create an unpublished bounty draft. Draft creation does not spend balance.", false, false, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPDraftInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			project, err := model.CreateOpenSourceBountyDraft(userId, bountyMCPDraft(input))
			return nil, bountyMCPOutput{Message: "Bounty draft created.", Data: project}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.delete_draft", "Delete a bounty draft", "Permanently delete an unpublished bounty draft. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPProjectInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.delete_draft", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				return nil, bountyMCPOutput{Message: "Bounty draft deletion already completed.", Data: replay}, nil
			}
			project, err := model.GetOpenSourceBountyProject(input.ProjectId)
			if err != nil || project.OwnerUserId != userId {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "draft is unavailable"})
			}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.delete_draft", input, fmt.Sprintf("Permanently delete draft %q?", project.Title))
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			err = model.DeleteOpenSourceBountyDraftWithMCPConfirmation(userId, input.ProjectId, *operation)
			return nil, bountyMCPOutput{Message: "Bounty draft deleted."}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.get", "Get bounty details", "Get a bounty, its viewer state, mutual ratings, and owner-only participant and ledger details.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPProjectInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			detail, err := model.GetOpenSourceBountyDetail(userId, input.ProjectId)
			return nil, bountyMCPOutput{Message: "Bounty detail loaded.", Data: detail}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.list", "List public bounties", "List the public bounty board in deterministic publication order. The board may be empty and has no default projects.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPListInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			items, total, err := model.ListOpenSourceBounties(userId, input.Page, input.PageSize)
			return nil, bountyMCPOutput{Message: "Bounty board loaded.", Data: map[string]any{"items": items, "total": total}}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.list_accepted", "List my accepted challenges", "List challenges accepted by the authenticated user, including mutual ratings and reputation aggregates.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			items, err := model.ListAcceptedOpenSourceBounties(userId)
			return nil, bountyMCPOutput{Message: "Accepted challenges loaded.", Data: items}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.list_owned", "List my bounty projects", "List all bounty drafts and published projects owned by the authenticated user.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			items, err := model.ListOwnedOpenSourceBounties(userId)
			return nil, bountyMCPOutput{Message: "Owned bounty projects loaded.", Data: items}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.list_disputes", "List bounty disputes", "List disputes involving the authenticated user. Administrators can list all disputes for third-party review.", true, false, true),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPDisputeListInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			items, err := model.ListOpenSourceBountyDisputesFiltered(userId, bountyMCPIsAdmin(userId), input.Status, input.Limit)
			return nil, bountyMCPOutput{Message: "Bounty disputes loaded.", Data: items}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.open_dispute", "Open a bounty dispute", "Escalate a bounty disagreement with an evidence snapshot for third-party administrator review. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPOpenDisputeInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.open_dispute", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				disputeId, _ := replay["dispute_id"].(float64)
				dispute, err := model.GetOpenSourceBountyDispute(userId, int(disputeId), false)
				return nil, bountyMCPOutput{Message: "Bounty dispute opening already completed.", Data: dispute}, bountyMCPError(err)
			}
			var evidenceSnapshot struct {
				model.OpenSourceBountyChallenge
				ProjectTitle     string `json:"project_title"`
				ProjectRules     string `json:"project_rules"`
				ProjectUpdatedAt int64  `json:"project_updated_at"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").Joins("JOIN open_source_bounty_projects project ON project.id = challenge.project_id").Where("challenge.id = ? AND (challenge.participant_user_id = ? OR project.owner_user_id = ?)", input.ChallengeId, userId, userId).Select("challenge.*, project.title AS project_title, project.rules AS project_rules, project.updated_at AS project_updated_at").Scan(&evidenceSnapshot).Error; err != nil || evidenceSnapshot.Id == 0 {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			message := fmt.Sprintf("Open a third-party dispute for challenge %d in %q with reason %q? The Issue, PR, completion note, reward and tip amounts, and mutual ratings will be frozen for administrators and both parties.", input.ChallengeId, evidenceSnapshot.ProjectTitle, input.Reason)
			confirmationPayload := map[string]any{"input": input, "evidence_snapshot": evidenceSnapshot}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.open_dispute", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			dispute, err := model.OpenOpenSourceBountyDisputeWithMCPConfirmation(userId, input.ChallengeId, input.Reason, input.Statement, *operation)
			return nil, bountyMCPOutput{Message: "Bounty dispute opened for third-party review.", Data: dispute}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.pause", "Pause bounty intake", "Pause a published bounty so it stops accepting new contributors.", false, false, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPProjectInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			project, err := model.SetOpenSourceBountyPaused(userId, input.ProjectId, true)
			return nil, bountyMCPOutput{Message: "Bounty paused.", Data: project}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.publish", "Publish and fund a bounty", "Deduct the gross listed price from the authenticated publisher, retain the public platform task fee, and escrow the net contributor rewards. Daily check-in rewards in the same balance can fund the listing. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPProjectInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.publish", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				projectId, _ := replay["project_id"].(float64)
				project, err := model.GetOpenSourceBountyProject(int(projectId))
				return nil, bountyMCPOutput{Message: "Bounty publication already completed.", Data: map[string]any{"project": project, "charged_quota": replay["charged_quota"]}, RemainingQuota: bountyMCPRemainingQuota(userId)}, bountyMCPError(err)
			}
			project, err := model.GetOpenSourceBountyProject(input.ProjectId)
			if err != nil || project.OwnerUserId != userId {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "draft is unavailable"})
			}
			charge, err := model.CalculateOpenSourceBountyPublicationCharge(project)
			if err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			}
			feeRecipientUserId := 0
			feeRecipientUsername := ""
			publisherNetDebit := charge.GrossQuota
			if charge.PlatformFeeQuota > 0 {
				recipient, err := model.GetOpenSourceBountyPlatformFeeRecipient()
				if err != nil {
					return nil, bountyMCPOutput{}, bountyMCPError(err)
				}
				feeRecipientUserId = recipient.Id
				feeRecipientUsername = recipient.Username
				if recipient.Id == userId {
					publisherNetDebit -= charge.PlatformFeeQuota
				}
			}
			message := fmt.Sprintf("Publish %q? This debits the gross listing total of %d (%d × %d), locks %d net reward quota (%d per approved fix) in escrow, and leaves a net balance decrease of %d.", project.Title, charge.GrossQuota, project.RewardQuota, project.RewardSlots, charge.EscrowQuota, charge.NetRewardQuota, publisherNetDebit)
			if charge.PlatformFeeQuota > 0 {
				message = fmt.Sprintf("Publish %q? This debits the gross listing total of %d (%d × %d), credits the public %0.2f%% platform fee of %d to super administrator %q (user %d), and locks %d net reward quota (%d per approved fix) in escrow. Your net balance decrease is %d.", project.Title, charge.GrossQuota, project.RewardQuota, project.RewardSlots, float64(charge.PlatformFeeRateBps)/100, charge.PlatformFeeQuota, feeRecipientUsername, feeRecipientUserId, charge.EscrowQuota, charge.NetRewardQuota, publisherNetDebit)
			}
			message += " Daily check-in rewards credited to the same balance can fund this listing."
			confirmationPayload := map[string]any{
				"input": input, "project": project, "charge": charge,
				"fee_recipient_user_id": feeRecipientUserId, "publisher_net_debit": publisherNetDebit,
				"remaining_quota": bountyMCPRemainingQuota(userId),
			}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.publish", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			operation.PlatformFeeRecipientUserId = feeRecipientUserId
			updated, charged, err := model.PublishOpenSourceBountyWithMCPConfirmation(userId, input.ProjectId, *operation)
			return nil, bountyMCPOutput{Message: "Bounty published and fully funded.", Data: map[string]any{"project": updated, "charged_quota": charged}, RemainingQuota: bountyMCPRemainingQuota(userId)}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.rate_owner", "Rate the publisher and verifier", "After approval or rejection, publish a 1-5 rating of the bounty publisher/verifier. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPRateOwnerInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.rate_owner", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				challengeId, _ := replay["challenge_id"].(float64)
				var challenge model.OpenSourceBountyChallenge
				err := model.DB.First(&challenge, int(challengeId)).Error
				return nil, bountyMCPOutput{Message: "Publisher/verifier rating already saved.", Data: &challenge}, bountyMCPError(err)
			}
			var participantProof struct {
				Id int `json:"id"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").
				Select("challenge.id").
				Where("challenge.id = ? AND challenge.participant_user_id = ?", input.ChallengeId, userId).
				Scan(&participantProof).Error; err != nil || participantProof.Id == 0 {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			message := fmt.Sprintf("Publish your %d/5 rating for the publisher/verifier of challenge %d? This rating and comment will be visible to both sides.", input.Score, input.ChallengeId)
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.rate_owner", input, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			challenge, err := model.RateOpenSourceBountyOwnerWithMCPConfirmation(userId, input.ChallengeId, input.Score, input.Comment, *operation)
			return nil, bountyMCPOutput{Message: "Publisher/verifier rating saved.", Data: challenge}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.reject", "Reject and rate a submission", "Reject a submission, publish a 1-5 contributor rating, and release its reward slot. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPReviewInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.reject", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				challengeId, _ := replay["challenge_id"].(float64)
				var challenge model.OpenSourceBountyChallenge
				err := model.DB.First(&challenge, int(challengeId)).Error
				return nil, bountyMCPOutput{Message: "Submission rejection and contributor rating already saved.", Data: &challenge}, bountyMCPError(err)
			}
			var ownerProof struct {
				Id int `json:"id"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").
				Joins("JOIN open_source_bounty_projects project ON project.id = challenge.project_id AND project.owner_user_id = ?", userId).
				Where("challenge.id = ?", input.ChallengeId).
				Select("challenge.id").
				Scan(&ownerProof).Error; err != nil || ownerProof.Id == 0 {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			message := fmt.Sprintf("Reject challenge %d, publicly rate the contributor %d/5, and release the reward slot?", input.ChallengeId, input.RatingScore)
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.reject", input, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			challenge, _, err := model.ReviewOpenSourceBountyChallengeWithMCPConfirmation(userId, input.ChallengeId, false, input.ReviewNote, input.RatingScore, input.RatingComment, *operation)
			return nil, bountyMCPOutput{Message: "Submission rejected and contributor rating saved.", Data: challenge}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.resume", "Resume bounty intake", "Resume a paused bounty so it accepts contributors again.", false, false, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPProjectInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			project, err := model.SetOpenSourceBountyPaused(userId, input.ProjectId, false)
			return nil, bountyMCPOutput{Message: "Bounty resumed.", Data: project}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.resolve_dispute", "Resolve a bounty dispute", "Administrator-only third-party resolution. Can deny a claim or force the escrowed reward payment when evidence proves the work met the bounty requirements. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPResolveDisputeInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if !bountyMCPIsAdmin(userId) {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "administrator access is required"})
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.resolve_dispute", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				disputeId, _ := replay["dispute_id"].(float64)
				dispute, err := model.GetOpenSourceBountyDispute(userId, int(disputeId), true)
				return nil, bountyMCPOutput{Message: "Bounty dispute resolution already completed.", Data: map[string]any{"dispute": dispute, "transferred_quota": replay["transferred_quota"]}}, bountyMCPError(err)
			}
			dispute, err := model.GetOpenSourceBountyDispute(userId, input.DisputeId, true)
			if err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			}
			expectedStatus := model.OpenSourceBountyDisputeResolvedDenied
			if input.Action == "pay" {
				expectedStatus = model.OpenSourceBountyDisputeResolvedPaid
			}
			if dispute.Status == expectedStatus {
				return nil, bountyMCPOutput{Message: "Bounty dispute was already resolved with this action.", Data: map[string]any{"dispute": dispute, "transferred_quota": 0}}, nil
			}
			message := fmt.Sprintf("Resolve dispute %d for %q with action %q? Paying transfers %d quota from the current %d escrow to @%s; denying closes the claim without payment. The public resolution is permanent.", input.DisputeId, dispute.ProjectTitle, input.Action, dispute.RewardQuota, dispute.CurrentProjectEscrowQuota, dispute.ParticipantUsername)
			confirmationPayload := map[string]any{"input": input, "dispute": dispute}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.resolve_dispute", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			dispute, transferred, err := model.ResolveOpenSourceBountyDisputeWithMCPConfirmation(userId, input.DisputeId, input.Action, input.Resolution, *operation)
			return nil, bountyMCPOutput{Message: "Bounty dispute resolved.", Data: map[string]any{"dispute": dispute, "transferred_quota": transferred}}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.submit", "Submit completion evidence", "Submit at least one matching GitHub Issue or pull request URL, optionally both, for direct review by the bounty publisher.", false, false, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPSubmitInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			challenge, err := model.SubmitOpenSourceBountyChallenge(userId, input.ProjectId, input.IssueUrl, input.PullRequestUrl, input.SubmissionNote)
			return nil, bountyMCPOutput{Message: "Bounty evidence submitted for review.", Data: challenge}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.tip", "Tip a contributor", "Transfer a discretionary, non-refundable tip from the publisher's own balance to a contributor without reducing escrow. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPTipInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.tip", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				challengeId, _ := replay["challenge_id"].(float64)
				var challenge model.OpenSourceBountyChallenge
				err := model.DB.First(&challenge, int(challengeId)).Error
				return nil, bountyMCPOutput{Message: "Contributor tip transfer already completed.", Data: map[string]any{"challenge": &challenge, "transferred_quota": replay["transferred_quota"]}, RemainingQuota: bountyMCPRemainingQuota(userId)}, bountyMCPError(err)
			}
			var recipient struct {
				Username           string `json:"username"`
				ParticipantUserId  int    `json:"participant_user_id"`
				ProjectId          int    `json:"project_id"`
				ChallengeStatus    string `json:"challenge_status"`
				ChallengeUpdatedAt int64  `json:"challenge_updated_at"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").Select("participant.username, challenge.participant_user_id, challenge.project_id, challenge.status AS challenge_status, challenge.updated_at AS challenge_updated_at").Joins("JOIN open_source_bounty_projects project ON project.id = challenge.project_id AND project.owner_user_id = ?", userId).Joins("JOIN users participant ON participant.id = challenge.participant_user_id").Where("challenge.id = ?", input.ChallengeId).Scan(&recipient).Error; err != nil || recipient.Username == "" {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			message := fmt.Sprintf("Send @%s a non-refundable %d quota tip from your own balance? This does not reduce bounty escrow or replace the formal reward.", recipient.Username, input.Quota)
			confirmationPayload := map[string]any{"input": input, "recipient": recipient, "remaining_quota": bountyMCPRemainingQuota(userId)}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.tip", confirmationPayload, message)
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			challenge, transferred, err := model.TipOpenSourceBountyChallengeWithMCPConfirmation(userId, input.ChallengeId, input.Quota, input.Note, *operation)
			return nil, bountyMCPOutput{Message: "Contributor tip transferred.", Data: map[string]any{"challenge": challenge, "transferred_quota": transferred}, RemainingQuota: bountyMCPRemainingQuota(userId)}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.update_draft", "Update a bounty draft", "Update an unpublished bounty draft. No balance is spent until publication.", false, false, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPUpdateDraftInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			project, err := model.UpdateOpenSourceBountyDraft(userId, input.ProjectId, bountyMCPDraft(input.bountyMCPDraftInput))
			return nil, bountyMCPOutput{Message: "Bounty draft updated.", Data: project}, bountyMCPError(err)
		})

	mcp.AddTool(server, bountyMCPTool("open_source_bounties.withdraw", "Withdraw from a challenge", "Withdraw the authenticated contributor from an accepted or submitted challenge. Requires explicit user confirmation.", false, true, false),
		func(ctx context.Context, request *mcp.CallToolRequest, input bountyMCPChallengeInput) (*mcp.CallToolResult, bountyMCPOutput, error) {
			userId, err := bountyMCPUserId(request)
			if err != nil {
				return nil, bountyMCPOutput{}, err
			}
			if replay, found, err := model.GetOpenSourceBountyMCPOperationResult(userId, "open_source_bounties.withdraw", request.Params.RequestState); err != nil {
				return nil, bountyMCPOutput{}, bountyMCPError(err)
			} else if found {
				challengeId, _ := replay["challenge_id"].(float64)
				var challenge model.OpenSourceBountyChallenge
				err := model.DB.First(&challenge, int(challengeId)).Error
				return nil, bountyMCPOutput{Message: "Challenge withdrawal already completed.", Data: &challenge}, bountyMCPError(err)
			}
			var participantProof struct {
				Id int `json:"id"`
			}
			if err := model.DB.Table("open_source_bounty_challenges AS challenge").
				Select("challenge.id").
				Where("challenge.id = ? AND challenge.participant_user_id = ?", input.ChallengeId, userId).
				Scan(&participantProof).Error; err != nil || participantProof.Id == 0 {
				return nil, bountyMCPOutput{}, bountyMCPError(&model.OpenSourceBountyError{Code: "OPEN_SOURCE_BOUNTY_FORBIDDEN", Message: "challenge is unavailable"})
			}
			pending, operation, err := bountyMCPConfirmedOperation(request, userId, "open_source_bounties.withdraw", input, fmt.Sprintf("Withdraw from challenge %d and release its reward slot?", input.ChallengeId))
			if err != nil || pending != nil {
				return pending, bountyMCPOutput{}, err
			}
			challenge, err := model.WithdrawOpenSourceBountyChallengeWithMCPConfirmation(userId, input.ChallengeId, *operation)
			return nil, bountyMCPOutput{Message: "Challenge withdrawn.", Data: challenge}, bountyMCPError(err)
		})
}

func newOpenSourceBountyMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name: "api.lmm.best-open-source-bounties", Version: common.Version,
	}, &mcp.ServerOptions{
		Instructions: "Manage the complete peer-to-peer open-source bounty lifecycle for the authenticated user. The board has no default projects. Every publisher funds the gross listed price from their own balance; the public administrator-configured platform fee is credited to the enabled super administrator and the remainder becomes contributor escrow. The publisher and contributor settle directly, while a third-party administrator intervenes only when either party opens a dispute. Daily check-in rewards credited to the publisher balance can fund listings. Never fabricate defects or evidence. Money, destructive, and public-rating actions return an input-required confirmation that must be shown to the user and explicitly accepted before retrying the tool.",
		Capabilities: &mcp.ServerCapabilities{},
	})
	server.AddPrompt(&mcp.Prompt{
		Name:        "open_source_bounty_operator",
		Title:       "Open-source bounty operator",
		Description: "Instructions for safely publishing, accepting, verifying, tipping, rating, and settling open-source bounties.",
	}, func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := strings.TrimSpace(`Use the connected api.lmm.best Open-source bounties MCP server to complete my request. Treat each bounty as a peer-to-peer transaction between its publisher and contributor; a third-party administrator intervenes only when either party opens a dispute. Do not invent bugs, Issues, pull requests, tests, scores, dispute facts, or review results. Read the current bounty, public administrator-configured task fee, and balance state before mutating it. Publishing debits the gross listed price from my balance, credits the public platform fee to the enabled super administrator account, and locks the remaining net contributor rewards in escrow. If I am that super administrator, report both the gross debit and fee credit as well as the resulting net balance decrease. Daily check-in rewards are credited to the same balance and can fund listings. The public board ranks listings by gross price per fix from highest to lowest. When the server returns an input-required confirmation for publishing, approval/payment, rejection, cancellation, closing/refunding, tipping, rating, opening or resolving a dispute, draft deletion, or withdrawal, show me the exact action, recipient, score, gross price, net reward, fee, evidence, and balance impact, then continue only after I explicitly confirm. Cancelling is available only to the publisher for an unsubmitted challenge; it releases the reserved reward slot but does not refund balance until the bounty is closed. Tips are independent, non-refundable transfers from my own balance and never reduce escrow. A contributor may submit a matching GitHub Issue URL, pull request URL, or both, plus an optional completion note. The bounty publisher reviews the completed work directly. At review time, record a truthful 1-5 contributor score and public evaluation; after review, contributors may rate the publisher/verifier, and both sides can see mutual ratings and historical averages. If a party disputes rejection or payment, preserve the linked Issue, pull request, completion note, reward and tip amounts, and mutual ratings for third-party administrator review. Administrators may force payment only from the remaining escrow after reviewing genuine evidence.`)
		return &mcp.GetPromptResult{
			Description: "Operate the authenticated user's open-source bounties end to end.",
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: text}},
			},
		}, nil
	})
	registerOpenSourceBountyMCPTools(server)
	return server
}

func NewOpenSourceBountyMCPHandler() http.Handler {
	server := newOpenSourceBountyMCPServer()
	streamable := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// The public endpoint is bearer-protected and reached through a loopback reverse proxy.
		DisableLocalhostProtection:   true,
		PropagateRequestCancellation: true,
	})
	verifier := func(ctx context.Context, token string, request *http.Request) (*auth.TokenInfo, error) {
		userId, err := model.VerifyOpenSourceBountyMCPToken(token)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid personal MCP token", auth.ErrInvalidToken)
		}
		return &auth.TokenInfo{
			UserID: strconv.Itoa(userId), Scopes: []string{"bounties:read", "bounties:write"},
			Extra: map[string]any{"protocol_version": openSourceBountyMCPProtocolVersion},
		}, nil
	}
	return auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		Scopes: []string{"bounties:read", "bounties:write"}, AllowMissingExpiration: true,
	})(streamable)
}
