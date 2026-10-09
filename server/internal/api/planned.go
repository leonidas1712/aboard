package api

import (
	"context"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// notProvided answers an operation of the API contract that this server doesn't have:
// tasks, asks, agent lines and files. A client reads 501 not_implemented as a server
// without the feature, and says so, rather than as a failure.
func notProvided(what string) error {
	return apierr.New(http.StatusNotImplemented, "not_implemented", "This server doesn't provide "+what+".",
		"Use a server whose GET /v1/info lists the feature.")
}

func (h *handlers) SetLine(context.Context, SetLineRequestObject) (SetLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) ClearLine(context.Context, ClearLineRequestObject) (ClearLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) SetMemberLine(context.Context, SetMemberLineRequestObject) (SetMemberLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) ClearMemberLine(context.Context, ClearMemberLineRequestObject) (ClearMemberLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) ApproveFile(context.Context, ApproveFileRequestObject) (ApproveFileResponseObject, error) {
	return nil, notProvided("file approvals")
}

func (h *handlers) RemoveFileApproval(context.Context, RemoveFileApprovalRequestObject) (RemoveFileApprovalResponseObject, error) {
	return nil, notProvided("file approvals")
}

func (h *handlers) GetAllowance(context.Context, GetAllowanceRequestObject) (GetAllowanceResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) SetAllowance(context.Context, SetAllowanceRequestObject) (SetAllowanceResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) ListApprovals(context.Context, ListApprovalsRequestObject) (ListApprovalsResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) RequestAdminAction(context.Context, RequestAdminActionRequestObject) (RequestAdminActionResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) AllowApproval(context.Context, AllowApprovalRequestObject) (AllowApprovalResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) DeclineApproval(context.Context, DeclineApprovalRequestObject) (DeclineApprovalResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) GetOnboardingReceipt(context.Context, GetOnboardingReceiptRequestObject) (GetOnboardingReceiptResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) ListPairingRequests(context.Context, ListPairingRequestsRequestObject) (ListPairingRequestsResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) CreatePairingRequest(context.Context, CreatePairingRequestRequestObject) (CreatePairingRequestResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) AcceptPairingRequest(context.Context, AcceptPairingRequestRequestObject) (AcceptPairingRequestResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) DeclinePairingRequest(context.Context, DeclinePairingRequestRequestObject) (DeclinePairingRequestResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) CancelPairingRequest(context.Context, CancelPairingRequestRequestObject) (CancelPairingRequestResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) VerifyPairingRoundTrip(context.Context, VerifyPairingRoundTripRequestObject) (VerifyPairingRoundTripResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) GetPairingRequest(context.Context, GetPairingRequestRequestObject) (GetPairingRequestResponseObject, error) {
	return nil, notProvided("onboarding")
}

func (h *handlers) CreatePairingCredential(context.Context, CreatePairingCredentialRequestObject) (CreatePairingCredentialResponseObject, error) {
	return nil, notProvided("pairing endpoint credentials")
}
