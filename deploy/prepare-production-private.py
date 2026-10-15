#!/usr/bin/env python3
"""Read the existing legacy tunnel token; never mutate Cloudflare routing."""
import argparse,json,os,pathlib,re,sys,urllib.request,urllib.error
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args): return None

def main():
    ap=argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--owner-env',type=pathlib.Path,required=True)
    ap.add_argument('--tunnel-token',type=pathlib.Path,required=True)
    args=ap.parse_args()
    email=os.environ.get('HELM_ADMIN_EMAIL','')
    if not re.fullmatch(r'[^\s@=]+@[^\s@=]+\.[^\s@=]+',email): raise ValueError('valid HELM_ADMIN_EMAIL required')
    token=os.environ.get('CLOUDFLARE_API_TOKEN','')
    if not token or any(c in token for c in '\r\n\x00'): raise ValueError('Cloudflare credential required')
    opener=urllib.request.build_opener(NoRedirect)
    base='https://api.cloudflare.com/client/v4/accounts/090ae73dce25f4eca9a53ee396fdc916/cfd_tunnel'
    def get(suffix):
        req=urllib.request.Request(base+suffix,headers={'Authorization':'Bearer '+token,'Accept':'application/json'})
        with opener.open(req,timeout=20) as r:
            body=r.read(1024*1024+1)
            if len(body)>1024*1024: raise ValueError('Cloudflare response too large')
            value=json.loads(body)
            if not value.get('success'): raise ValueError('Cloudflare read rejected')
            return value['result']
    tunnels=[x for x in get('?is_deleted=false&per_page=100') if x.get('name')=='roadmap-homelab']
    if len(tunnels)!=1 or not re.fullmatch(r'[a-f0-9-]{36}',tunnels[0].get('id','')):raise ValueError('expected exactly one production legacy tunnel')
    tunnel_token=get('/'+tunnels[0]['id']+'/token')
    if not isinstance(tunnel_token,str) or not tunnel_token or any(c in tunnel_token for c in '\r\n\x00'):raise ValueError('invalid tunnel credential')
    owner=(pathlib.Path(__file__).parent/'tailnet-owner-production.env.example').read_text().replace('REPLACE_WITH_EXISTING_ADMIN_EMAIL',email)
    owner+='\nHELM_LUNA_ENABLED=true\nHELM_LUNA_MODEL=gpt-5.6-luna\nHELM_LUNA_EFFORT=medium\n'
    for path,content in ((args.owner_env,owner),(args.tunnel_token,tunnel_token+'\n')):
        path.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
        fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'w') as f:f.write(content)
    print('Prepared fixed private production profile and existing legacy connector; no Cloudflare mutations')
if __name__=='__main__':
    try:main()
    except Exception: print('Private production input preparation failed; verify protected credentials, explicit owner and existing tunnel',file=sys.stderr);sys.exit(1)
