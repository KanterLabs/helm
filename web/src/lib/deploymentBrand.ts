const BETA_HOSTNAME = 'beta-helm.home.shanekanterman.dev';

export function deploymentName(hostname: string): 'Helm' | 'HELM-BETA' {
  const canonicalHostname = hostname.trim().toLowerCase().replace(/\.$/, '');
  return canonicalHostname === BETA_HOSTNAME ? 'HELM-BETA' : 'Helm';
}

export function applyDeploymentBrand(documentRoot: Document, hostname: string): void {
  const name = deploymentName(hostname);
  documentRoot.title = name;
  documentRoot
    .querySelector('meta[name="apple-mobile-web-app-title"]')
    ?.setAttribute('content', name);
}
