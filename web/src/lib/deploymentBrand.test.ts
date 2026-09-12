import { describe, expect, it } from 'vitest';
import { applyDeploymentBrand, deploymentName } from './deploymentBrand';

describe('deployment branding', () => {
  it('brands only the private beta hostname as HELM-BETA', () => {
    expect(deploymentName('beta-helm.home.shanekanterman.dev')).toBe('HELM-BETA');
    expect(deploymentName('BETA-HELM.HOME.SHANEKANTERMAN.DEV.')).toBe('HELM-BETA');
    expect(deploymentName('tc.shanekanterman.dev')).toBe('Helm');
    expect(deploymentName('beta-helm.home.shanekanterman.dev.example')).toBe('Helm');
  });

  it('updates the document title and installed-app title together', () => {
    document.head.innerHTML = '<title>Helm</title><meta name="apple-mobile-web-app-title" content="Helm">';

    applyDeploymentBrand(document, 'beta-helm.home.shanekanterman.dev');

    expect(document.title).toBe('HELM-BETA');
    expect(document.querySelector('meta[name="apple-mobile-web-app-title"]')?.getAttribute('content')).toBe(
      'HELM-BETA'
    );
  });
});
