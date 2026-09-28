import { afterEach } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'

// A spec that mounts a component with a polling `setInterval` (e.g.
// OnboardingWizard.vue's onboarding-state poll) leaves it running against the
// shared mocked api module for the rest of the file if nothing ever unmounts
// the wrapper — no test here called `wrapper.unmount()` in an `afterEach`, so
// a leaked interval from one test could consume a call a LATER test's mock
// only returns once (see onboarding-wizard-verify-states.spec.ts). Auto-unmount
// every wrapper after each test closes that gap suite-wide instead of adding
// the same afterEach to every spec file individually.
enableAutoUnmount(afterEach)
