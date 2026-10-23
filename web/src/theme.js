import { reactive } from 'vue'

const STORAGE_KEY = 'mikvace_theme'

export const themeOptions = [
  { value: 'dark', label: '石墨', description: '暖黑底色，低眩光控制台' },
  { value: 'light', label: '暖纸', description: '米白纸感，适合明亮环境' },
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
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', next === 'light' ? '#f3efe6' : '#0d0d0c')
}

export function setTheme(value) {
  const next = normalize(value)
  localStorage.setItem(STORAGE_KEY, next)
  applyTheme(next)
}

export function toggleTheme() {
  setTheme(theme.current === 'light' ? 'dark' : 'light')
}
