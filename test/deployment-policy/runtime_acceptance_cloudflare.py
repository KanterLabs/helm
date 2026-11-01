"""Disposable HTTPS JWKS issuer exercises the retained real Access verifier."""
from __future__ import annotations
import base64
from datetime import datetime, timedelta, timezone
import ipaddress
import json
import os
import subprocess
import time


def encoded(raw):
    return base64.urlsafe_b64encode(raw).decode().rstrip('=')


def material(root):
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes, serialization
    from cryptography.hazmat.primitives.asymmetric import rsa
    from cryptography.x509.oid import NameOID
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, 'localhost')])
    now = datetime.now(timezone.utc)
    cert = (x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key())
            .serial_number(x509.random_serial_number()).not_valid_before(now-timedelta(minutes=1))
            .not_valid_after(now+timedelta(hours=2))
            .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
            .add_extension(x509.SubjectAlternativeName([x509.DNSName('localhost'), x509.IPAddress(ipaddress.ip_address('127.0.0.1'))]), critical=False)
            .sign(key, hashes.SHA256()))
    (root/'cert.pem').write_bytes(cert.public_bytes(serialization.Encoding.PEM))
    (root/'key.pem').write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    public = key.public_key().public_numbers()
    encode_int = lambda n: encoded(n.to_bytes((n.bit_length()+7)//8, 'big'))
    jwks = {'keys': [{'kty': 'RSA', 'kid': 'tc336-disposable', 'use': 'sig', 'alg': 'RS256', 'n': encode_int(public.n), 'e': encode_int(public.e)}]}
    (root/'jwks.json').write_text(json.dumps(jwks))
    for name in ['cert.pem', 'key.pem', 'jwks.json']:
        (root/name).chmod(0o640)
    return key


def assertion(key, email):
    from cryptography.hazmat.primitives import hashes
    from cryptography.hazmat.primitives.asymmetric import padding
    now = int(time.time())
    header = encoded(json.dumps({'alg': 'RS256', 'typ': 'JWT', 'kid': 'tc336-disposable'}).encode())
    claims = encoded(json.dumps({'iss': 'https://localhost:8123', 'aud': ['tc336-api'], 'iat': now-5,
                                'nbf': now-5, 'exp': now+300, 'email': email, 'sub': email}).encode())
    signed = (header+'.'+claims).encode()
    return signed.decode()+'.'+encoded(key.sign(signed, padding.PKCS1v15(), hashes.SHA256()))


def exercise(app, identities, observations):
    key = material(app.root)
    try:
        app.start()
        subprocess.run(['docker', 'exec', '--detach', app.container, '/acceptance/client', 'jwks'], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        time.sleep(.2)
        app.scenario = 'configured-email-demotion'
        admin_header = {'Cf-Access-Jwt-Assertion': assertion(key, identities['admin']['email'])}
        configured_header = {'Cf-Access-Jwt-Assertion': assertion(key, identities['configured']['email'])}
        app.call('GET', '/api/v1/members', 200, extra=admin_header)
        app.call('PATCH', '/api/v1/members/'+identities['configured']['id'], 200,
                 {'role': 'member', 'project_ids': []}, extra=admin_header,
                 template='/api/v1/members/{actor_id}')
        # Repeated requests execute actual configured-email reconciliation.
        for _ in range(3):
            app.call('GET', '/api/v1/members', 403, extra=configured_header)
            app.call('GET', '/api/v1/projects', 200, expected={'data_count': 0}, extra=configured_header)
        app.scenario = 'pending-disabled-human'
        pending_header = {'Cf-Access-Jwt-Assertion': assertion(key, 'tc336-new-pending@example.test')}
        app.call('GET', '/api/v1/projects', 403, extra=pending_header)
        app.call('GET', '/api/v1/export', 403, extra=pending_header)
        disabled_header = {'Cf-Access-Jwt-Assertion': assertion(key, identities['disabled']['email'])}
        app.call('GET', '/api/v1/projects', 401, extra=disabled_header)
    finally:
        observations.extend(app.observations)
        app.stop()
        for name in ['key.pem', 'cert.pem', 'jwks.json']:
            (app.root/name).unlink(missing_ok=True)
