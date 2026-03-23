<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div>
      <h1 class="text-3xl font-bold">Send Feedback</h1>
      <p class="text-base-content/70 mt-1">Help us improve MCPProxy by sharing your thoughts, reporting bugs, or requesting features.</p>
    </div>

    <!-- Success Alert -->
    <div v-if="submitted" class="alert alert-success">
      <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Thanks! Your feedback was submitted.</h3>
        <p v-if="issueUrl" class="text-sm mt-1">
          <a :href="issueUrl" target="_blank" rel="noopener noreferrer" class="link link-hover underline">
            View the GitHub Issue
          </a>
        </p>
      </div>
      <button class="btn btn-sm btn-ghost" @click="resetForm">Send Another</button>
    </div>

    <!-- Error Alert -->
    <div v-if="errorMessage" class="alert alert-error">
      <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ errorMessage }}</span>
    </div>

    <!-- Feedback Form -->
    <div v-if="!submitted" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <form @submit.prevent="submitFeedback" class="space-y-4">
          <!-- Category -->
          <div class="form-control w-full">
            <label class="label">
              <span class="label-text font-medium">Category</span>
            </label>
            <select v-model="form.category" class="select select-bordered w-full">
              <option value="bug">Bug Report</option>
              <option value="feature">Feature Request</option>
              <option value="other">Other</option>
            </select>
          </div>

          <!-- Message -->
          <div class="form-control w-full">
            <label class="label">
              <span class="label-text font-medium">Message <span class="text-error">*</span></span>
            </label>
            <textarea
              v-model="form.message"
              class="textarea textarea-bordered w-full h-40"
              placeholder="Describe the bug, feature request, or other feedback..."
              required
              minlength="10"
              maxlength="5000"
            ></textarea>
            <label class="label">
              <span class="label-text-alt" :class="{ 'text-error': form.message.length > 0 && form.message.length < 10 }">
                {{ form.message.length }}/5000 characters (minimum 10)
              </span>
            </label>
          </div>

          <!-- Email -->
          <div class="form-control w-full">
            <label class="label">
              <span class="label-text font-medium">Email</span>
            </label>
            <input
              v-model="form.email"
              type="email"
              class="input input-bordered w-full"
              placeholder="For follow-up (optional)"
            />
          </div>

          <!-- Submit -->
          <div class="form-control mt-6">
            <button
              type="submit"
              class="btn btn-primary"
              :disabled="submitting || form.message.length < 10"
            >
              <span v-if="submitting" class="loading loading-spinner loading-sm"></span>
              <span v-else>Submit Feedback</span>
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- GitHub Link -->
    <div class="text-sm text-base-content/60">
      You can also
      <a
        href="https://github.com/smart-mcp-proxy/mcpproxy-go/issues/new"
        target="_blank"
        rel="noopener noreferrer"
        class="link link-hover link-primary"
      >open an issue on GitHub</a>.
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import api from '@/services/api'

const form = reactive({
  category: 'bug',
  message: '',
  email: '',
})

const submitting = ref(false)
const submitted = ref(false)
const errorMessage = ref('')
const issueUrl = ref('')

async function submitFeedback() {
  if (form.message.length < 10) return

  submitting.value = true
  errorMessage.value = ''

  try {
    const payload: { category: string; message: string; email?: string } = {
      category: form.category,
      message: form.message,
    }
    if (form.email) {
      payload.email = form.email
    }

    const response = await api.submitFeedback(payload)
    if (response.success) {
      submitted.value = true
      issueUrl.value = response.data?.issue_url || ''
    } else {
      errorMessage.value = response.error || 'Failed to submit feedback. Please try again.'
    }
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : 'An unexpected error occurred.'
  } finally {
    submitting.value = false
  }
}

function resetForm() {
  form.category = 'bug'
  form.message = ''
  form.email = ''
  submitted.value = false
  errorMessage.value = ''
  issueUrl.value = ''
}
</script>
