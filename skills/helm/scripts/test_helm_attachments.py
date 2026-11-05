"""Real HTTP CLI journeys using synthetic images only.

Failure cases recorded before implementation: unsupported capabilities must not
write; bad files/too many images must fail before staging; lost upload responses
must reconcile metadata before replay; partial batches must never publish;
server replays must match content; binaries must never reach stdout; downloads
must be explicit, bounded, and refuse overwrites; original progress behavior
must remain unchanged. Captured JSON is the repeatable artifact for each run.
"""
from __future__ import annotations
import base64
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit
PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgaPgPAAIDAYAkYfWXAAAAAElFTkSuQmCC')
SCRIPT = Path(__file__).with_name('helm.py')

class Fixture(BaseHTTPRequestHandler):

    def log_message(self, *_args):
        pass

    def send(self, status, body, content_type='application/json'):
        raw = json.dumps(body).encode() if content_type == 'application/json' else body
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        state = self.server.state
        path = urlsplit(self.path).path
        state['gets'].append(self.path)
        if path.endswith('/capabilities'):
            return self.send(404 if state['unsupported'] else 200, {'enabled': True, 'max_file_bytes': 10485760, 'max_images_per_update': 4, 'types': ['image/png', 'image/jpeg', 'image/webp']})
        if path.endswith('/content'):
            return self.send(200, PNG, 'image/png')
        if path.endswith('/thumbnail'):
            return self.send(200, PNG, 'image/png')
        if path.endswith('/attachments'):
            return self.send(200, {'data': list(state['attachments'].values()), 'next_cursor': ''})
        if '/attachments/' in path:
            return self.send(200, state['attachments'][path.rsplit('/', 1)[1]])
        return self.send(200, {'id': 'task-1', 'key': 'TC-359', 'version': 3, 'claimed_by': 'agent-1'})

    def do_POST(self):
        state = self.server.state
        path = urlsplit(self.path).path
        raw = self.rfile.read(int(self.headers.get('Content-Length', '0')))
        state['posts'].append({'path': path, 'raw': raw, 'headers': dict(self.headers)})
        if path.endswith('/attachments'):
            if state['fail_second'] and len(state['attachments']) == 1:
                return self.send(503, {'error': {'message': 'synthetic unavailable'}})
            ident = state['keys'].get(self.headers.get('Idempotency-Key'))
            if ident is None:
                ident = 'image-' + str(len(state['attachments']) + 1)
                state['keys'][self.headers.get('Idempotency-Key')] = ident
                item = {'id': ident, 'task_id': 'task-1', 'project_id': 'project-1', 'actor_id': 'agent-1', 'filename': 'proof.png', 'content_type': 'image/png', 'size_bytes': len(PNG), 'sha256': hashlib.sha256(PNG).hexdigest(), 'status': 'staged', 'version': 1, 'width': 1, 'height': 1, 'caption': 'Proof'}
                state['attachments'][ident] = item
            if state['lose_response']:
                state['lose_response'] = False
                self.close_connection = True
                return
            return self.send(201, state['attachments'][ident])
        body = json.loads(raw)
        if path.endswith('/progress'):
            return self.send(200, {'id': 'task-1', 'version': 4, **body})
        for ident in body.get('attachment_ids', []):
            state['attachments'][ident]['status'] = 'published'
        return self.send(201, {'id': 'comment-1', **body})

    def do_DELETE(self):
        state = self.server.state
        item = state['attachments'][self.path.rsplit('/', 1)[1]]
        state['delete_headers'] = dict(self.headers)
        item['status'] = 'deleted'
        self.send(200, item)

class AttachmentJourneys(unittest.TestCase):

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.image = self.root / 'proof.png'
        self.image.write_bytes(PNG)
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        self.server.state = {'gets': [], 'posts': [], 'attachments': {}, 'keys': {}, 'unsupported': False, 'lose_response': False, 'fail_second': False}
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def cli(self, *args, ok=True):
        env = {**os.environ, 'HELM_URL': f'http://127.0.0.1:{self.server.server_port}', 'HELM_TOKEN': 'synthetic-token', 'HELM_CONFIG': str(self.root / 'missing')}
        for key in ('TC_ROADMAP_URL', 'TC_ROADMAP_TOKEN', 'ROADMAP_TOKEN', 'TC_ROADMAP_CONFIG'):
            env.pop(key, None)
        run = subprocess.run([sys.executable, str(SCRIPT), *map(str, args)], env=env, text=True, capture_output=True)
        captured = {'args': list(map(str, args)), 'returncode': run.returncode, 'stdout': run.stdout, 'stderr': run.stderr}
        if (artifact := os.environ.get('HELM_TEST_ARTIFACT')):
            with open(artifact, 'a', encoding='utf-8') as out:
                out.write(json.dumps(captured) + '\n')
        self.assertEqual(run.returncode, 0 if ok else 1, run.stderr)
        self.assertNotIn('synthetic-token', run.stdout + run.stderr)
        return json.loads(run.stdout if ok else run.stderr)

    def upload(self):
        return self.cli('attachments', 'upload', '--task', 'TC-359', '--image', self.image, '--caption', 'Proof', '--operation-id', 'image-test')

    def test_upload_list_download_delete_journey(self):
        item = self.upload()['attachments'][0]
        self.assertEqual(item['status'], 'staged')
        post = self.server.state['posts'][0]
        self.assertIn(PNG, post['raw'])
        self.assertNotIn(str(self.root).encode(), post['raw'])
        self.assertIn('filename="proof.png"', post['raw'].decode('latin1'))
        self.assertTrue(post['headers'].get('Idempotency-Key'))
        self.assertEqual(self.cli('attachments', 'list', '--task', 'TC-359')['data'][0]['id'], item['id'])
        dest = self.root / 'download.png'
        self.cli('attachments', 'download', '--task', 'TC-359', '--attachment', item['id'], '--output', dest)
        self.assertEqual(dest.read_bytes(), PNG)
        self.cli('attachments', 'download', '--task', 'TC-359', '--attachment', item['id'], '--output', dest, ok=False)
        self.assertEqual(self.cli('attachments', 'delete', '--task', 'TC-359', '--attachment', item['id'], '--operation-id', 'delete-test')['status'], 'deleted')
        self.assertEqual(self.server.state['delete_headers']['If-Match'], '"v1"')

    def test_comment_and_structured_progress_publish_ids(self):
        result = self.cli('comment', '--task', 'TC-359', '--message', 'Verified UI', '--image', self.image, '--caption', 'Proof', '--operation-id', 'comment-test')
        self.assertEqual(result['comment']['attachment_ids'], ['image-1'])
        result = self.cli('progress', '--task', 'TC-359', '--message', 'Verified UI', '--state', 'verifying', '--image', self.image, '--caption', 'Proof', '--operation-id', 'progress-test')
        self.assertEqual(result['task']['attachment_ids'], ['image-2'])

    def test_staged_upload_can_be_published_by_id(self):
        item = self.upload()['attachments'][0]
        result = self.cli('comment', '--task', 'TC-359', '--message', 'Reviewed proof', '--attachment-id', item['id'], '--operation-id', 'publish-staged')
        self.assertEqual(result['comment']['attachment_ids'], [item['id']])
        self.assertEqual(len(self.server.state['attachments']), 1)

    def test_metadata_mismatch_and_batch_limits_do_not_publish(self):
        result = self.cli('comment', '--task', 'TC-359', '--message', 'Proof', '--image', self.image, '--caption', 'Different caption', ok=False)
        self.assertIn('match', result['error']['message'])
        self.assertFalse(any((p['path'].endswith('/comments') for p in self.server.state['posts'])))
        self.server.state['posts'].clear()
        self.cli('attachments', 'upload', '--task', 'TC-359', *sum((['--image', self.image] for _ in range(5)), []), ok=False)
        self.assertEqual(self.server.state['posts'], [])

    def test_lost_response_reconciles_before_retry(self):
        self.server.state['lose_response'] = True
        self.upload()
        self.assertEqual(len(self.server.state['attachments']), 1)
        self.assertTrue(any(('staged=true' in path for path in self.server.state['gets'])))

    def test_partial_batch_never_posts_comment(self):
        self.server.state['fail_second'] = True
        result = self.cli('comment', '--task', 'TC-359', '--message', 'Proof', '--image', self.image, '--image', self.image, '--caption', 'Proof', '--caption', 'Proof', '--operation-id', 'partial-test', ok=False)
        self.assertEqual(result['error']['staged_attachment_ids'], ['image-1'])
        self.assertFalse(any((p['path'].endswith('/comments') for p in self.server.state['posts'])))

    def test_unsupported_and_local_validation_do_not_upload(self):
        self.server.state['unsupported'] = True
        result = self.cli('comment', '--task', 'TC-359', '--message', 'Proof', '--image', self.image, ok=False)
        self.assertIn('unsupported', result['error']['message'].lower())
        self.assertEqual(self.server.state['posts'], [])
        self.server.state['unsupported'] = False
        bad = self.root / 'bad.png'
        bad.write_text('not an image')
        self.cli('attachments', 'upload', '--task', 'TC-359', '--image', bad, ok=False)
        self.assertEqual(self.server.state['posts'], [])

    def test_invalid_progress_and_legacy_image_do_not_stage(self):
        for extra in ([], ['--state', 'working', '--completed', '3', '--total', '2']):
            self.cli('progress', '--task', 'TC-359', '--message', 'Proof', '--image', self.image, *extra, ok=False)
        self.assertEqual(self.server.state['posts'], [])
if __name__ == '__main__':
    unittest.main()
