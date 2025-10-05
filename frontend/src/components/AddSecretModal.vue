<template>
  <dialog :open="show" class="modal">
    <div class="modal-box max-w-2xl">
      <form @submit.prevent="handleSubmit">
        <h3 class="font-bold text-lg mb-4">Add New Secret</h3>

        <!-- Secret Name -->
        <div class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Secret Name</span>
          </label>
          <input
            type="text"
            v-model="formData.name"
            placeholder="e.g., my-api-key"
            class="input input-bordered"
            required
          />
          <label class="label">
            <span class="label-text-alt">Use only letters, numbers, and hyphens</span>
          </label>
        </div>

        <!-- Secret Value -->
        <div class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Secret Value</span>
          </label>
          <input
            type="password"
            v-model="formData.value"
            placeholder="Enter secret value"
            class="input input-bordered"
            required
          />
        </div>

        <!-- Configuration Reference Preview -->
        <div v-if="formData.name" class="alert alert-info mb-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <div class="font-semibold">Configuration reference:</div>
            <code>${keyring:{{ formData.name }}}</code>
          </div>
        </div>

        <!-- Error Display -->
        <div v-if="error" class="alert alert-error mb-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ error }}</span>
        </div>

        <!-- Actions -->
        <div class="modal-action">
          <button type="button" @click="handleClose" class="btn btn-ghost">Cancel</button>
          <button type="submit" class="btn btn-primary" :disabled="loading || !formData.name || !formData.value">
            <span v-if="loading" class="loading loading-spinner loading-sm"></span>
            {{ loading ? 'Adding...' : 'Add Secret' }}
          </button>
        </div>
      </form>
    </div>
    <form method="dialog" class="modal-backdrop" @click="handleClose">
      <button>close</button>
    </form>
  </dialog>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import apiClient from '@/services/api'
import { useSystemStore } from '@/stores/system'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'added'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const systemStore = useSystemStore()

const formData = reactive({
  name: '',
  value: ''
})

const loading = ref(false)
const error = ref('')

async function handleSubmit() {
  error.value = ''
  loading.value = true

  try {
    const response = await apiClient.setSecret(formData.name, formData.value)
    if (response.success) {
      systemStore.addToast({
        type: 'success',
        title: 'Secret Added',
        message: `${formData.name} has been added successfully. Use in config: ${response.data?.reference}`
      })

      emit('added')
      handleClose()
    } else {
      error.value = response.error || 'Failed to add secret'
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add secret'
  } finally {
    loading.value = false
  }
}

function handleClose() {
  // Reset form
  formData.name = ''
  formData.value = ''
  error.value = ''

  emit('close')
}
</script>
