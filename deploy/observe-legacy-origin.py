#!/usr/bin/env python3
"""Record bounded, private migration evidence; never retire routes automatically."""
import datetime,json,os,pathlib,subprocess,tempfile,urllib.request
ROOT=pathlib.Path('/var/lib/roadmap/operations')
STATE=ROOT/'legacy-origin-observation.json'
def main():
 ROOT.mkdir(parents=True,exist_ok=True,mode=0o750)
 now=datetime.datetime.now(datetime.timezone.utc)
 previous=json.loads(STATE.read_text()) if STATE.exists() else {}
 start=previous.get('started_at','2026-10-15T16:36:44+00:00')
 since=previous.get('checked_at',start)
 result=subprocess.run(['journalctl','--unit=helm.service','--since='+since,'--until='+now.isoformat(),'--output=json','--no-pager'],capture_output=True,text=True,check=True)
 count=0;last=previous.get('last_legacy_request_at')
 for line in result.stdout.splitlines():
  try:
   event=json.loads(line);message=json.loads(event.get('MESSAGE',''))
   if message.get('msg')!='legacy_hostname_request':continue
   timestamp=datetime.datetime.fromtimestamp(int(event['__REALTIME_TIMESTAMP'])/1000000,datetime.timezone.utc)
   # Avoid counting the inclusive journal since boundary twice.
   if timestamp<=datetime.datetime.fromisoformat(since):continue
   count+=1;last=timestamp.isoformat()
  except (ValueError,TypeError,KeyError):continue
 with urllib.request.urlopen('http://127.0.0.1:8080/readyz',timeout=5) as response:ready=json.load(response).get('ready') is True
 status={'started_at':start,'checked_at':now.isoformat(),'not_before_retirement_review':(datetime.datetime.fromisoformat(start)+datetime.timedelta(days=30)).isoformat(),'legacy_requests':previous.get('legacy_requests',0)+count,'last_legacy_request_at':last,'backend_ready':ready,'retirement_requires_review':True,'routes_changed':False}
 fd,tmp=tempfile.mkstemp(prefix='.legacy-',dir=ROOT)
 try:
  os.fchmod(fd,0o640)
  with os.fdopen(fd,'w') as f:json.dump(status,f,sort_keys=True);f.write('\n');f.flush();os.fsync(f.fileno())
  os.replace(tmp,STATE)
 finally:
  if os.path.exists(tmp):os.unlink(tmp)
 print(json.dumps({'backend_ready':ready,'new_legacy_requests':count,'routes_changed':False}))
if __name__=='__main__':main()
