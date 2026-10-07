package api

import "context"

func (h *handlers) RenamePerson(ctx context.Context, req RenamePersonRequestObject) (RenamePersonResponseObject, error) {
	changed, err := h.svc.RenamePerson(ctx, principal(ctx), req.Handle, req.Body.Handle)
	if err != nil {
		return nil, err
	}
	return convert[RenamePerson200JSONResponse](map[string]any{"person": personOf(changed.Person), "changed": changed.Changed})
}
