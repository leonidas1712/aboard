package delivery

import "context"

// Combined delivery and explicit board routing are available in this build.
const multiSeatEnabled = true

// ReasonExtensionOutdated identifies a session whose extension needs updating.
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

func (d *Daemon) setHandoffProblem(key SessionKey, failing bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handoffProblems == nil {
		d.handoffProblems = map[SessionKey]bool{}
	}
	if failing {
		d.handoffProblems[key] = true
	} else {
		delete(d.handoffProblems, key)
	}
}

func (d *Daemon) setGeneration(agent AgentRef, generation uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.generations == nil {
		d.generations = map[AgentKey]uint64{}
	}
	d.generations[agent.Key()] = generation
}

func (d *Daemon) generation(agent AgentRef) uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generations[agent.Key()]
}
