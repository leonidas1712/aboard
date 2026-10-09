package sqlite

func (t *tx) SetHumanMidturn(id, policy string) error {
	return t.exec("UPDATE humans SET midturn_policy = ? WHERE id = ?", policy, id)
}

func (t *tx) SetAgentMidturn(id string, policy *string) error {
	return t.exec("UPDATE members SET midturn_policy = ? WHERE id = ?", policy, id)
}
