import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import i18n from './i18n'
import { createRPCPlugin, initRPCConnection } from './plugins/rpc'
import './styles/main.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(i18n)
app.use(createRPCPlugin())

app.mount('#app')

initRPCConnection().catch((err) => {
  console.error('Failed to initialize RPC connection:', err)
})
