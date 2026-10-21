import { reactive } from 'vue'

const STORAGE_KEY = 'mikvace_theme'

export const themeOptions = [
  { value: 'dark', label: '深色', description: '默认的深蓝配色' },
  { value: 'light', label: '白色', description: '明亮的浅色界面' },
]

function normalize(value) {
  return themeOptions.some((t) => t.value === value) ? value : 'dark'
}

export const theme = reactive({ current: normalize(localStorage.getItem(STORAGE_KEY)) })

export function applyTheme(value) {
  const next = normalize(value)
  theme.current = next
  document.documentElement.dataset.theme = next
  document.documentElement.style.colorScheme = next === 'light' ? 'light' : 'dark'
}

export function setTheme(value) {
  const next = normalize(value)
  localStorage.setItem(STORAGE_KEY, next)
  applyTheme(next)
}

export function toggleTheme() {
  setTheme(theme.current === 'light' ? 'dark' : 'light')
}
