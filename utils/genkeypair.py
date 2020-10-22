#!/usr/bin/env python3

# genkeypair - a quick-and-dirty utility to create ed25519 signing key pairs and curve25519
# 	encryption key pairs

# Released under the terms of the GPLv3 license
# ©2019-2020 Jon Yoder <jsyoder@mailfence.com>

import base64
import hashlib
from os import path
import sys

import nacl.signing
import nacl.public
import nacl.secret
import nacl.utils

def generate_encpair(filename):
	'''Creates a asymmetric keypair and saves it to a file in Base85 encoding'''
	keypair = nacl.public.PrivateKey.generate()
	
	hasher=hashlib.blake2b(digest_size=32)
	hasher.update(keypair.public_key.encode())
	publicHash = "BLAKE2B-256:" + base64.b85encode(hasher.digest()).decode()
	if not filename:
		print('Keypair type: encryption\r\n')
		print('public: %s' % base64.b85encode(keypair.public_key.encode()).decode())
		print('public hash: %s' % publicHash)
		print('private: %s' % base64.b85encode(keypair.encode()).decode())
		return

	if path.exists(filename):
		response = input("%s exists. Overwrite? [y/N]: " % filename)
		if not response or response.casefold()[0] != 'y':
			return
	try:
		out = open(filename, 'wb')

		out.write(b'Keypair type: encryption\r\n')
		out.write(b'public: ' + base64.b85encode(keypair.public_key.encode()) + b'\r\n')
		out.write(b'private: ' + base64.b85encode(keypair.encode()) + b'\r\n')
	except Exception as e:
		print('Unable to save %s: %s' % (filename, e))


def generate_signpair(filename):
	'''Creates a asymmetric signing keypair and saves it to a file in Base85 encoding'''
	keypair = nacl.signing.SigningKey.generate()
	
	if not filename:
		print('Keypair type: signing\r\n')
		print('verify: %s' % base64.b85encode(keypair.verify_key.encode()).decode())
		print('signing: %s' % base64.b85encode(keypair.encode()).decode())
		return
	
	if path.exists(filename):
		response = input("%s exists. Overwrite? [y/N]: " % filename)
		if not response or response.casefold()[0] != 'y':
			return
	try:
		out = open(filename, 'wb')

		out.write(b'Keypair type: signing\r\n')
		out.write(b'verify:' + base64.b85encode(keypair.verify_key.encode()) + b'\r\n')
		out.write(b'sign:' + base64.b85encode(keypair.encode()) + b'\r\n')
	except Exception as e:
		print('Unable to save %s: %s' % (filename, e))


if __name__ == '__main__':
	if len(sys.argv) not in [2,3] or sys.argv[1].casefold() not in ['sign', 'encrypt']:
		print("Usage: %s <sign|encrypt> <filename>" % path.basename(sys.argv[0]))
		sys.exit(0)
	
	keyfile = ''
	if len(sys.argv) == 3:
		keyfile = sys.argv[2]

	if sys.argv[1].casefold() == 'encrypt':
		generate_encpair(keyfile)
	else:
		generate_signpair(keyfile)
