package runtime

const defaultEventBuffer = 16

// SubscribeEvents registers a new subscriber and returns a channel that will receive runtime events.
// Callers must not close the returned channel; use UnsubscribeEvents when finished.
func (r *Runtime) SubscribeEvents() chan Event {
	ch := make(chan Event, defaultEventBuffer)
	r.eventMu.Lock()
	r.eventSubs[ch] = struct{}{}
	r.eventMu.Unlock()
	return ch
}

// UnsubscribeEvents removes the subscriber and closes the channel.
func (r *Runtime) UnsubscribeEvents(ch chan Event) {
	r.eventMu.Lock()
	if _, ok := r.eventSubs[ch]; ok {
		delete(r.eventSubs, ch)
		close(ch)
	}
	r.eventMu.Unlock()
}

func (r *Runtime) publishEvent(evt Event) {
	r.eventMu.RLock()
	for ch := range r.eventSubs {
		select {
		case ch <- evt:
		default:
		}
	}
	r.eventMu.RUnlock()
}

func (r *Runtime) emitServersChanged(reason string, extra map[string]any) {
	payload := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		payload[k] = v
	}
	payload["reason"] = reason
	r.publishEvent(newEvent(EventTypeServersChanged, payload))
}

func (r *Runtime) emitConfigReloaded(path string) {
	payload := map[string]any{"path": path}
	r.publishEvent(newEvent(EventTypeConfigReloaded, payload))
}

func (r *Runtime) emitConfigSaved(path string) {
	payload := map[string]any{"path": path}
	r.publishEvent(newEvent(EventTypeConfigSaved, payload))
}

func (r *Runtime) emitSecretsChanged(operation string, secretName string, extra map[string]any) {
	payload := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		payload[k] = v
	}
	payload["operation"] = operation
	payload["secret_name"] = secretName
	r.publishEvent(newEvent(EventTypeSecretsChanged, payload))
}
