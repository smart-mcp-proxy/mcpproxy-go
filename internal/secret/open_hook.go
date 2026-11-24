package secret

// beforeSecretOpenHook runs in tests between policy checks and opening a secret
// file. Production code never sets it.
var beforeSecretOpenHook func()

// SetBeforeSecretOpenHookForTest installs beforeSecretOpenHook. Pass nil to clear.
func SetBeforeSecretOpenHookForTest(hook func()) {
	beforeSecretOpenHook = hook
}

func invokeBeforeSecretOpenHook() {
	if beforeSecretOpenHook != nil {
		beforeSecretOpenHook()
	}
}
