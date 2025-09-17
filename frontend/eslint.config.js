module.exports = {
  root: true,
  env: {
    node: true,
    browser: true,
    es2022: true
  },
  extends: [
    'plugin:vue/vue3-essential',
    '@vue/eslint-config-typescript',
    'eslint:recommended'
  ],
  parserOptions: {
    ecmaVersion: 'latest',
    sourceType: 'module'
  },
  rules: {
    'vue/multi-word-component-names': 'off',
    '@typescript-eslint/no-unused-vars': 'warn'
  },
  ignorePatterns: ['dist/**', 'coverage/**']
}