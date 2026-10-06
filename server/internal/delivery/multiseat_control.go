package delivery

import "context"

// The second binding stays disabled until combined delivery and routing are verified.
const multiSeatEnabled = false

const ReasonExtensionOutdated = "extension_outdated"

func (d *Daemon) bindingPreflight(ctx context.Context, key SessionKey, agent AgentRef) Response {
	s := d.session(key, false)
	if s == nil {
		return Response{V: ProtocolVersion}
	}
	reply := make(chan Response, 1)
	s.mail.put(sessionMsg{preflight: &agent, reply: reply})
	select {
	case r := <-reply:
		return r
	case <-ctx.Done():
		return errorResponse("daemon_not_running", "The delivery daemon is stopping.", "Run the command again.")
	}
}

func (d *Daemon) setExtensionProblem(key SessionKey, outdated bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.extensionProblems == nil {
		d.extensionProblems = map[SessionKey]bool{}
	}
	if outdated {
		d.extensionProblems[key] = true
	} else {
		delete(d.extensionProblems, key)
	}
}
