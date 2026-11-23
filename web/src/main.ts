import { createApp } from 'vue';
import App from './App.vue';
import { createAppRouter } from './router';
import './styles/goldie.css';
import './styles/base.css';

// Mount once the first route is settled: the guard may send the visitor to
// /signin, and the shell should not flash first.
const router = createAppRouter();
const app = createApp(App).use(router);
void router.isReady().then(() => app.mount('#app'));
