import { expect, test } from '@playwright/test';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// Failure cases recorded before integration fixes: token scope bypass on image
// reads, upload replay duplicating blobs, partial publication, lost historical
// progress images, unpublished image theft, delete without audit, and unsafe ZIPs.
// One-time synthetic tokens stay in memory; never capture HTTP traces/videos.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

test('scoped CLI uploads, retries and progress retain evidence and revoke reads', async ({ request }, info) => {
  test.setTimeout(90_000);
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const headers = { Origin: origin };
  const id = Date.now().toString(36).toUpperCase();
  const project = await (await request.post('/api/v1/projects', { headers, data: { key: `CLI${id}`.slice(0,16), name: 'CLI image journey' } })).json();
  const columns = await (await request.get(`/api/v1/projects/${project.id}/columns`)).json();
  const create = async (title: string) => (await (await request.post(`/api/v1/projects/${project.id}/tasks`, { headers, data: { title, column_id: columns.data[0].id } })).json());
  const task = await create('Agent screenshot evidence');
  const other = await create('Wrong publication target');
  const agent = await (await request.post('/api/v1/agents', { headers, data: { name: `Screenshot agent ${id}`, project_ids: [project.id] } })).json();
  const issued = await (await request.post(`/api/v1/agents/${agent.id}/tokens`, { headers, data: { name: 'Synthetic screenshot runner', scopes: ['projects:read','tasks:read','tasks:write','tasks:claim','events:read'], project_ids: [project.id] } })).json();
  const bearer = { ...headers, Authorization: `Bearer ${issued.token}` };
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNwaDgAAAKEAYEml6crAAAAAElFTkSuQmCC','base64');
  const directory = mkdtempSync(join(tmpdir(),'helm-image-cli-'));
  const file = join(directory,'proof.png'); writeFileSync(file,png);
  const env = { PATH: process.env.PATH || '/usr/bin:/bin', HELM_CONFIG: join(directory,'absent.json'), HELM_URL: origin, HELM_TOKEN: issued.token, PYTHONDONTWRITEBYTECODE: '1' };
  const cli = (...args: string[]) => {
    const run = spawnSync('python3',[resolve('../skills/helm/scripts/helm.py'),...args],{env,encoding:'utf8',timeout:20_000});
    expect(run.status, run.stderr.replaceAll(issued.token,'[redacted]')).toBe(0);
    expect(run.stdout.includes(issued.token)).toBe(false);
    return JSON.parse(run.stdout);
  };
  try {
    const staged = cli('attachments','upload','--task',task.key,'--image',file,'--caption','Agent inventory screen','--operation-id',`upload-${id}`);
    const image = staged.attachments[0];
    const replay = cli('attachments','upload','--task',task.key,'--image',file,'--caption','Agent inventory screen','--operation-id',`upload-${id}`);
    expect(replay.attachments[0].id).toBe(image.id);
    const reader = await (await request.post(`/api/v1/agents/${agent.id}/tokens`, { headers, data: { name: 'Independent reader', scopes: ['tasks:read'], project_ids: [project.id] } })).json();
    const readerHeaders = { Authorization: `Bearer ${reader.token}` };
    // A token belongs to the same actor, so staged visibility is deliberately
    // checked with a distinct actor rather than another credential.
    const outsider = await (await request.post('/api/v1/agents', { headers, data: { name: `Other actor ${id}`, project_ids: [project.id] } })).json();
    const outsiderToken = await (await request.post(`/api/v1/agents/${outsider.id}/tokens`, { headers, data: { name: 'Other actor reader', scopes: ['tasks:read'], project_ids: [project.id] } })).json();
    const privateRead = await request.get(`/api/v1/tasks/${task.id}/attachments/${image.id}/content`, { headers: { Authorization: `Bearer ${outsiderToken.token}` } });
    expect(privateRead.status()).toBe(404);
    const wrong = await request.post(`/api/v1/tasks/${other.id}/comments`, {headers:bearer,data:{body:'Wrong image',attachment_ids:[image.id]}});
    expect(wrong.status()).toBeGreaterThanOrEqual(400);
    expect((await (await request.get(`/api/v1/tasks/${other.id}/comments`)).json()).data).toHaveLength(0);
    const claim = await request.post(`/api/v1/tasks/${task.id}/claim`, {headers:{...bearer,'If-Match':`"v${task.version}"`},data:{lease_seconds:600}});
    expect(claim.status()).toBe(200);
    cli('progress','--task',task.key,'--state','working','--phase','UI implemented','--message','Inventory screenshot attached','--attachment-id',image.id,'--operation-id',`work-${id}`);
    cli('progress','--task',task.key,'--state','verifying','--phase','Review','--message','Checking the next step','--operation-id',`work-${id}`);
    const history = await (await request.get(`/api/v1/tasks/${task.id}/timeline`,{headers:bearer})).json();
    const evidence = history.data.filter((item: {progress?: {attachments?: unknown[]}}) => item.progress?.attachments?.length);
    expect(evidence).toHaveLength(1);
    expect(evidence[0].progress.attachments[0].id).toBe(image.id);
    const content = `/api/v1/tasks/${task.id}/attachments/${image.id}/content`;
    expect((await request.head(content, { headers: readerHeaders })).status()).toBe(200);
    const range = await request.get(content, { headers: { ...readerHeaders, Range: 'bytes=0-7' } });
    expect(range.status()).toBe(206);
    expect(await range.body()).toEqual(png.subarray(0, 8));
    expect((await request.delete(`/api/v1/tokens/${reader.id}`, { headers })).status()).toBe(204);
    expect((await request.get(content, { headers: readerHeaders })).status()).toBe(401);
    expect((await request.head(content, { headers: readerHeaders })).status()).toBe(401);
    const unrelated = await (await request.post('/api/v1/projects', { headers, data: { key: `DENY${id}`.slice(0,16), name: 'Unrelated image scope' } })).json();
    const restricted = await (await request.post('/api/v1/agents', { headers, data: { name: `Restricted ${id}`, project_ids: [unrelated.id] } })).json();
    const restrictedToken = await (await request.post(`/api/v1/agents/${restricted.id}/tokens`, { headers, data: { name: 'Restricted reader', scopes: ['tasks:read'], project_ids: [unrelated.id] } })).json();
    const restrictedHeaders = { Authorization: `Bearer ${restrictedToken.token}`, Range: 'bytes=0-7' };
    for (const suffix of ['', '/content', '/thumbnail']) {
      expect((await request.get(`/api/v1/tasks/${task.id}/attachments/${image.id}${suffix}`, { headers: restrictedHeaders })).status()).toBe(403);
    }
    expect((await request.head(content, { headers: restrictedHeaders })).status()).toBe(403);
    const output = join(directory,'download.png');
    cli('attachments','download','--task',task.key,'--attachment',image.id,'--output',output);
    expect(readFileSync(output)).toEqual(png);
    cli('attachments','delete','--task',task.key,'--attachment',image.id);
    const removed = await request.get(`/api/v1/tasks/${task.id}/attachments/${image.id}/content`,{headers:bearer});
    expect(removed.status()).toBe(404);
    const events = await (await request.get(`/api/v1/events?project=${project.id}&limit=200`,{headers:bearer})).json();
    expect(events.data.some((event: {type:string;actor_id:string}) => event.type==='attachment.deleted' && event.actor_id===agent.id)).toBe(true);
    await info.attach('agent-image-journey.json',{contentType:'application/json',body:JSON.stringify({upload_replayed_once:true,wrong_task_rejected:true,progress_image_retained:true,original_bytes_verified:true,deleted_image_denied:true})});
  } finally { rmSync(directory,{recursive:true,force:true}); }
});

test('portable image bundles validate before mutation and replay one imported copy', async ({ request }, info) => {
  const origin = new URL(process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
  const headers = { Origin: origin };
  const suffix = Date.now().toString(36).toUpperCase();
  const project = async (prefix: string) => (await (await request.post('/api/v1/projects',{headers,data:{key:`${prefix}${suffix}`.slice(0,16),name:`Bundle ${prefix}`}})).json());
  const source = await project('BSA'); const destination = await project('BDA');
  const columns = await (await request.get(`/api/v1/projects/${source.id}/columns`)).json();
  const task = await (await request.post(`/api/v1/projects/${source.id}/tasks`,{headers,data:{title:'Portable screenshot',column_id:columns.data[0].id}})).json();
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNwaDgAAAKEAYEml6crAAAAAElFTkSuQmCC','base64');
  const image = await (await request.post(`/api/v1/tasks/${task.id}/attachments`,{headers:{...headers,'Idempotency-Key':`bundle-image-${suffix}`},multipart:{file:{name:'proof.png',mimeType:'image/png',buffer:png}}})).json();
  expect((await request.post(`/api/v1/tasks/${task.id}/comments`,{headers,data:{body:'',attachment_ids:[image.id]}})).status()).toBe(201);
  const exported = await request.get(`/api/v1/projects/${source.id}/export?format=bundle`);
  expect(exported.status()).toBe(200);
  const bundle = await exported.body();
  const url = `/api/v1/import/bundle?target_project=${destination.id}&conflict=remap`;
  const taskCount = async () => (await (await request.get(`/api/v1/projects/${destination.id}/tasks`)).json()).data.length;
  const before = await taskCount();
  const corrupt = (mode: string) => {
    const code = `import io,sys,warnings,zipfile\nwarnings.simplefilter('ignore')\nsrc=zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read()));out=io.BytesIO()\nwith zipfile.ZipFile(out,'w') as dst:\n for item in src.infolist():\n  raw=src.read(item)\n  if sys.argv[1]=='corrupt' and item.filename.startswith('objects/'):raw=raw[:-1]+bytes([raw[-1]^1])\n  dst.writestr(item.filename,raw)\n if sys.argv[1]=='path':dst.writestr('../outside',b'no')\n if sys.argv[1]=='duplicate':dst.writestr('manifest.json',src.read('manifest.json'))\nsys.stdout.buffer.write(out.getvalue())`;
    const result = spawnSync('python3',['-c',code,mode],{input:bundle,maxBuffer:8*1024*1024});
    expect(result.status).toBe(0); return result.stdout;
  };
  for (const mode of ['corrupt','path','duplicate']) {
    const rejected = await request.post(url,{headers:{...headers,'Content-Type':'application/zip'},data:corrupt(mode)});
    expect(rejected.status()).toBeGreaterThanOrEqual(400);
    expect(rejected.status()).toBeLessThan(500);
    expect(await taskCount()).toBe(before);
  }
  const review = await request.post(`${url}&dry_run=true`,{headers:{...headers,'Content-Type':'application/zip'},data:bundle});
  expect(review.status()).toBe(200); expect((await review.json()).dry_run).toBe(true);
  expect(await taskCount()).toBe(before);
  const publishHeaders = {...headers,'Content-Type':'application/zip','Idempotency-Key':`bundle-apply-${suffix}`};
  const imported = await request.post(url,{headers:publishHeaders,data:bundle});
  expect(imported.status()).toBe(200);
  const replay = await request.post(url,{headers:publishHeaders,data:bundle});
  expect(replay.status()).toBe(200); expect(await replay.json()).toEqual(await imported.json());
  expect(await taskCount()).toBe(before+1);
  const tasks = await (await request.get(`/api/v1/projects/${destination.id}/tasks`)).json();
  const copied = tasks.data.find((value:{title:string}) => value.title==='Portable screenshot');
  const comments = await (await request.get(`/api/v1/tasks/${copied.id}/comments`)).json();
  expect(comments.data[0].body).toBe(''); expect(comments.data[0].attachments).toHaveLength(1);
  const restored = comments.data[0].attachments[0];
  expect((await request.get(`/api/v1/tasks/${copied.id}/attachments/${restored.id}/content`)).status()).toBe(200);
  await info.attach('portable-image-roundtrip.json',{contentType:'application/json',body:JSON.stringify({corrupt_hash_rejected:true,traversal_rejected:true,duplicate_rejected:true,dry_run_nonmutating:true,replay_single_copy:true,image_only_comment_preserved:true})});
});
