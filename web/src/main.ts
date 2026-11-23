import { createApp } from 'vue';
import App from './App.vue';
import { createAppRouter } from './router';
import './styles/goldie.css';
import './styles/base.css';

// Mount at once: App shows a loading state until the first route is settled
// (the guard may send the visitor to /signin, and the shell should not flash
// first, nor the page stay blank while the first session check is in flight).
const router = createAppRouter();
const app = createApp(App).use(router);
app.mount('#app');
