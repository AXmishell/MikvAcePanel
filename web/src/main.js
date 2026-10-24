import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import './assets/main.css'
import { applyTheme, theme } from './theme'
import { tooltip } from './directives/tooltip'

applyTheme(theme.current)

createApp(App).use(router).directive('tip', tooltip).mount('#app')
