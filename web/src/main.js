import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import './assets/main.css'
import { applyTheme, theme } from './theme'

applyTheme(theme.current)

createApp(App).use(router).mount('#app')
