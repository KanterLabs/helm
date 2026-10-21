import '@fontsource/inter/latin-400.css';
import '@fontsource/inter/latin-500.css';
import '@fontsource/inter/latin-600.css';
import '@fontsource/jetbrains-mono/latin-500.css';
import { mount } from 'svelte';
import App from './App.svelte';
import './app.css';
import { applyDeploymentBrand } from './lib/deploymentBrand';
import { registerPwa } from './lib/pwa';

applyDeploymentBrand(document, window.location.hostname);
registerPwa();

const app = mount(App, {
  target: document.getElementById('app') as HTMLElement
});

export default app;
