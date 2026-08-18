#!/usr/bin/env python3
"""Generate VAPID P-256 ECDSA key pair for Web Push."""
import base64
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.backends import default_backend

private_key = ec.generate_private_key(ec.SECP256R1(), default_backend())
public_key = private_key.public_key()

private_numbers = private_key.private_numbers()
private_bytes = private_numbers.private_value.to_bytes(32, 'big')

public_numbers = public_key.public_numbers()
x = public_numbers.x.to_bytes(32, 'big')
y = public_numbers.y.to_bytes(32, 'big')
public_bytes = b'\x04' + x + y

print('VAPID_PUBLIC_KEY=' + base64.urlsafe_b64encode(public_bytes).rstrip(b'=').decode())
print('VAPID_PRIVATE_KEY=' + base64.urlsafe_b64encode(private_bytes).rstrip(b'=').decode())
