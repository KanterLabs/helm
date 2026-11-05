#!/usr/bin/env python3
"""Run retained immutable Helm images, privately; emits diagnostic E2E receipts.

This intentionally cannot mint deployment-policy acceptance bundles. A trusted
operator must review full scope, real-copy evidence and registry provenance.
"""
from __future__ import annotations
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import uuid

import runtime_acceptance_storage as storage

HELPER = r'''package main
import("bytes";"encoding/json";"io";"net/http";"os";"time";"strconv";"syscall")
type Request struct {Method string; Path string; Headers map[string]string; Body json.RawMessage}
func main(){
 if len(os.Args)>1 && os.Args[1]=="share" {
  gid,e:=strconv.Atoi(os.Args[2]);if e!=nil{panic(e)}
  for _,suffix:=range []string{"-wal","-shm"}{p:="/acceptance/roadmap.db"+suffix;if st,e:=os.Stat(p);e==nil{if int(st.Sys().(*syscall.Stat_t).Gid)!=gid{if e=os.Chown(p,-1,gid);e!=nil{panic(e)}};if st.Mode().Perm()!=0660{if e=os.Chmod(p,0660);e!=nil{panic(e)}}}};return
 }
 if len(os.Args)>1 && os.Args[1]=="jwks" {
  raw,e:=os.ReadFile("/acceptance/jwks.json");if e!=nil{panic(e)}
  http.HandleFunc("/certs",func(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");w.Write(raw)})
  panic(http.ListenAndServeTLS("127.0.0.1:8123","/acceptance/cert.pem","/acceptance/key.pem",nil))
 }
 var q Request;if e:=json.NewDecoder(os.Stdin).Decode(&q);e!=nil{panic(e)}
 r,e:=http.NewRequest(q.Method,"http://127.0.0.1:8080"+q.Path,bytes.NewReader(q.Body));if e!=nil{panic(e)}
 for k,v:=range q.Headers{r.Header.Set(k,v)}
 c:=http.Client{Timeout:10*time.Second};s,e:=c.Do(r);if e!=nil{panic(e)}
 defer s.Body.Close();raw,e:=io.ReadAll(io.LimitReader(s.Body,16<<20));if e!=nil{panic(e)}
 json.NewEncoder(os.Stdout).Encode(map[string]any{"status":s.StatusCode,"body":string(raw),"cookies":s.Header.Values("Set-Cookie")})
}
'''


def command(args, **kwargs):
    return subprocess.run(args, check=True, text=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, **kwargs).stdout.strip()


class Runtime:
    def __init__(self, image, root, network, release_sha, mode='local'):
        self.image, self.root, self.network, self.release_sha = image, root, network, release_sha
        self.mode, self.container = mode, None
        self.observations = []
        self.scenario = 'readiness'
        self.headers = {}

    def start(self):
        env = {'HELM_AUTH_MODE': self.mode, 'HELM_ADDR': '127.0.0.1:8080',
               'HELM_DB': '/acceptance/roadmap.db', 'HELM_SECURE_COOKIES': 'false',
               'HELM_PUBLIC_ORIGIN': 'http://acceptance.test', 'HELM_ADMIN_EMAIL': 'tc336-configured@example.test',
               'HELM_RELEASE_SHA': self.release_sha, 'HELM_LUNA_ENABLED': 'false'}
        if self.mode == 'cloudflare':
            env.update(HELM_SECURE_COOKIES='true', HELM_PUBLIC_ORIGIN='https://acceptance.test',
                       HELM_CLOUDFLARE_ISSUER='https://localhost:8123',
                       HELM_CLOUDFLARE_JWKS_URL='https://localhost:8123/certs',
                       HELM_CF_ACCESS_AUDIENCES='tc336-ui,tc336-api',
                       SSL_CERT_FILE='/acceptance/cert.pem')
        args = ['docker', 'run', '--detach', '--network', self.network, '--group-add', str(os.getgid()),
                '--mount', f'type=bind,src={self.root},dst=/acceptance', '--read-only', '--tmpfs', '/tmp']
        for key, value in env.items():
            args += ['--env', key+'='+value]
        self.container = command(args+[self.image])
        inspected = json.loads(command(['docker', 'inspect', self.container]))[0]
        assert inspected['Image'] == self.image, 'runtime image ID mismatch'
        assert not inspected['HostConfig']['PortBindings'], 'published port prohibited'
        assert inspected['Config']['Entrypoint'] == ['/usr/local/bin/helm'], 'entrypoint changed'
        for _ in range(60):
            try:
                self.raw('GET', '/api/v1/auth/status')
                command(['docker', 'exec', self.container, '/acceptance/client', 'share', str(os.getgid())])
                return self
            except (subprocess.CalledProcessError, ValueError):
                time.sleep(.25)
        raise AssertionError('retained image failed to start (private logs not exported)')

    def stop(self):
        if self.container:
            command(['docker', 'rm', '-f', '-v', self.container])
            self.container = None

    def raw(self, method, path, body=None, cookie=None, bearer=None, extra=None):
        headers = {'Origin': 'https://acceptance.test' if self.mode == 'cloudflare' else 'http://acceptance.test',
                   'Content-Type': 'application/json', **self.headers}
        if cookie:
            headers['Cookie'] = 'roadmap_session='+cookie
        if bearer:
            headers['Authorization'] = 'Bearer '+bearer
        if extra:
            headers.update(extra)
        req = {'Method': method, 'Path': path, 'Headers': headers, 'Body': body}
        raw = json.loads(command(['docker', 'exec', '-i', self.container, '/acceptance/client'], input=json.dumps(req)))
        return raw['status'], json.loads(raw['body']) if raw['body'] else {}, raw['cookies']

    def call(self, method, path, want, body=None, cookie=None, bearer=None, project=None, expected=None, template=None, extra=None):
        status, response, cookies = self.raw(method, path, body, cookie, bearer, extra)
        projection = {}
        if isinstance(response, dict) and isinstance(response.get('error'), dict):
            projection['error_code'] = response['error'].get('code')
        if project is not None:
            projection['contains_forbidden_project'] = any(item in json.dumps(response) for item in (project if isinstance(project, list) else [project]))
        if expected is not None:
            for key, value in expected.items():
                assert key in {'data_count', 'key', 'task_total', 'issues', 'my_work'}, 'non-allowlisted receipt projection'
                if key == 'data_count':
                    projection['data_count'] = len(response.get('data', [])) if isinstance(response.get('data'), list) else -1
                elif key in {'task_total', 'issues', 'my_work'}:
                    projection[key] = response.get(key) if type(response.get(key)) is int else '<unexpected>'
                else:
                    # This key is created by the synthetic recovery HTTP write.
                    projection[key] = response.get(key) if response.get(key) == value else '<unexpected>'
        expect = {}
        if 'error_code' in projection:
            code = 'forbidden' if want == 403 else 'unauthorized'
            if want == 401 and path == '/api/v1/auth/login':
                code = 'invalid_credentials'
            if want == 403 and self.mode == 'cloudflare' and self.scenario == 'pending-disabled-human':
                code = 'membership_required'
            if want == 403 and bearer and method == 'POST':
                code = 'insufficient_scope'
            expect['error_code'] = code
        if project is not None:
            expect['contains_forbidden_project'] = False
        if expected is not None:
            expect.update(expected)
        passed = status == want and projection == expect
        self.observations.append({'scenario': self.scenario, 'assertion': self.scenario+': '+method+' '+(template or path.split('?')[0])+' matches independently expected status and allowlisted response projection',
                                  'passed': passed, 'exchange': {'method': method, 'path': template or path.split('?')[0],
                                  'status': status, 'expected_status': want, 'response': projection, 'expected_response': expect}})
        assert passed, f'{self.scenario}: {method} {template or path.split("?")[0]} status={status} expected={want}; sanitized projection={projection}'
        return response, cookies

    def login(self, who, want=200):
        body, cookies = self.call('POST', '/api/v1/auth/login', want,
                                  {'email': who['email'], 'password': who['password']})
        if want != 200:
            return None
        assert cookies, 'real login did not issue cookie'
        return cookies[0].split(';', 1)[0].split('=', 1)[1]


def helper(root, go):
    source = root/'client.go'
    source.write_text(HELPER)
    command([go, 'build', '-o', str(root/'client'), str(source)],
            env={**os.environ, 'GOTOOLCHAIN': 'local', 'GO111MODULE': 'off', 'CGO_ENABLED': '0'})
    (root/'client').chmod(0o750)
    source.unlink()


def run(args):
    import runtime_acceptance_journeys as journeys
    import runtime_acceptance_cloudflare as cloudflare
    root = args.output.resolve()
    root.mkdir(parents=True, exist_ok=False, mode=0o700)
    network = 'tc336-'+uuid.uuid4().hex[:12]
    command(['docker', 'network', 'create', '--internal', network])
    assert json.loads(command(['docker', 'network', 'inspect', network]))[0]['Internal'] is True
    summary = {'kind': 'diagnostic-immutable-runtime-e2e', 'input': 'private-lane-copies' if args.schema35 else 'generated-populated-fixtures',
               'policy_acceptance': False, 'runs': []}
    try:
        for role, image, sha in [('candidate', args.candidate, args.candidate_sha), ('recovery', args.recovery, args.recovery_sha)]:
            assert image.startswith('sha256:') and len(image) == 71, 'exact retained image ID/manifest digest required'
            for version in [35, 40]:
                work = root/f'{role}-schema{version}'
                work.mkdir(mode=0o770)
                db = work/'roadmap.db'
                source = args.schema35 if version == 35 else args.schema40
                if source:
                    storage.copy_database(source.resolve(), db, version)
                else:
                    storage.generated(db, version, Path(__file__).resolve().parents[2])
                identities = storage.identities(db)
                before = storage.snapshot(db, version)
                helper(work, args.go)
                db.chmod(0o660)
                app = Runtime(image, work, network, sha)
                report = {'role': role, 'schema_before': version, 'image_id': image, 'source_sha': sha,
                          'result': 'failed', 'observations': app.observations}
                summary['runs'].append(report)
                try:
                    app.start()
                    report['storage'] = storage.preserved(db, before, args.candidate_schema if role == 'candidate' else args.recovery_schema)
                    journeys.exercise(app, identities, db)
                    app.stop()
                    cloudflare.exercise(Runtime(image, work, network, sha, 'cloudflare'), identities, report['observations'])
                    report['result'] = 'passed'
                finally:
                    app.stop()
                    (root/f'{role}-schema{version}-receipts.json').write_text(json.dumps(report, indent=2)+'\n')
        journeys.rollforward(args, root, network, Runtime, summary)
        summary['result'] = 'passed'
    except Exception as exc:
        summary['result'] = 'failed'
        summary['failure'] = str(exc) if isinstance(exc, AssertionError) else type(exc).__name__
        raise
    finally:
        (root/'summary.json').write_text(json.dumps(summary, indent=2)+'\n')
        command(['docker', 'network', 'rm', network])


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--candidate', required=True)
    p.add_argument('--recovery', required=True)
    p.add_argument('--candidate-sha', required=True)
    p.add_argument('--candidate-schema', type=int, default=41)
    p.add_argument('--recovery-schema', type=int, default=41)
    p.add_argument('--recovery-sha', required=True)
    p.add_argument('--schema35', type=Path)
    p.add_argument('--schema40', type=Path)
    p.add_argument('--output', required=True, type=Path)
    p.add_argument('--go', default='/home/shane/.cache/helm-go-1.25.14/go/bin/go')
    args = p.parse_args()
    if not (41 <= args.candidate_schema <= 10000 and 41 <= args.recovery_schema <= 10000):
        p.error('expected schemas must be explicit supported versions at or above41')
    if bool(args.schema35) != bool(args.schema40):
        p.error('both independently captured schema35 and schema40 copies are required')
    run(args)


if __name__ == '__main__':
    main()
