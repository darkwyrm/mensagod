
from integration_setup import setup_test, connect, validate_uuid

# Workspace ID : 11111111-1111-1111-1111-111111111111
# Friendly Address : 
# Status : active
# Password : SandstoneAgendaTricycle
# Local Password Hash : $argon2id$v=19$m=65536,t=2,p=1$ew5lqHA5z38za+257DmnTA$0LWVrI2r7XCqdcCYkJLok65qussSyhN5TTZP+OTgzEI
# Server Password Hash : $argon2id$v=19$m=65536,t=2,p=1$stqWnAj7604YLtPk/oEd+w$KET7Q1OjDz8jymJhYLVvkxgcwB7uWCuxpZuUqwooLrM
# Identity Verify.b85 : 23k&JP-4sNQ{HKYn)7#j`(Fw}pv@WhJ2`^!Cs57)
# Identity Signing.b85 : nuV^-YSY~shq9cFjn8HRs4+AI&^{Fvkat1>4$G$Z
# Contact Public.b85 : q~NVs$%Z82g7ZfniK3@!N+FrzcYJnawDdyYa!}@W
# Contact Private.b85 : oLdi)O~WWC={1pe8W{e&E_OiW&J?j&E9@}mO(Iha
# Broadcast.b85 : VHMt@bDw|w-+KC8|IEn!;o|Li3gVG#rof@)gi&C1
# System.b85 : iNdT1WT&G5gx=;<G=2*N$EP$+TCf4gz@2rY$<UCr
# Folder Map.b85 : I|u|QIIZ_=+i#k#aA-_uP=E`>-1+g9X%jL}Z8tbR
# Message Folder : b32e9eb1-33bd-4a35-a8ce-eed23f5c9c4a
# Contacts Folder : 7c22e94b-65fa-43d2-9b27-9e202ef813cd
# Calendar Folder : 0603506a-395d-43e5-af11-b946e358e9a1
# Tasks Folder : 0a4171bd-8a5e-4e61-a6b1-1657418f311f
# Files Folder : c4f853de-4735-4889-85f0-044c84f6142f
# Attachments Folder : d24679e3-da6e-4730-b9f8-be376c6dcf19
# Social Folder : 98327262-cfba-454b-bd51-b24c9dfdb3ad
# Device #1 ID : c64eed46-1a2a-4baf-81b7-ac28c6284de9
# Device #1 Key Type : curve25519
# Device #1 Public.b85 : @X~msiMmBq0nsNnn0%~x{M|NU_{?<Wj)cYybdh&Z
# Device #1 Private.b85 : W30{oJ?w~NBbj{F8Ag4~<bcWy6_uQ{i{X?NDq4^l
# Device #2 ID : 7e780f98-529a-4b2d-92eb-1f874976ba62
# Device #2 Key Type : curve25519
# Device #2 Public.b85 : @B8FD;u#}Sd4jD*+LtpzRhxeVKW7h$Q4uA$`%AxM
# Device #2 Private.b85 : u3x80VFNg9V+`Jok5%$H(9os=$>W;W%qG8Y+>_Cz
# Device #3 ID : 92dc8c59-18e4-4472-b41f-95e2c3ebe31d
# Device #3 Key Type : curve25519
# Device #3 Public.b85 : K_3$mL8X@P(x!u7PkWnncDvp(XSYFJ0&qU*6!Hc`
# Device #3 Private.b85 : 3>Y=(Mg*|xcxl#}p+L0IcxqB%cncjQEA`R|1&r0_
# Device #4 ID : 1eb935eb-623f-4319-8a65-dc9ffa102bf5
# Device #4 Key Type : curve25519
# Device #4 Public.b85 : ij>F4A$$uckNj|M@7kSf=SKx#?K?l9!veJ`VTuYM
# Device #4 Private.b85 : RX!@Zj1N@`6CMffaqYIWE2JXit{$tg5U*TUHt$y)
# Device #5 ID : 36340705-3b30-4507-8e3b-4433287933ba
# Device #5 Key Type : curve25519
# Device #5 Public.b85 : YlT2>26y#gaIQOM8x*wO7Hc~+jflG#Din3VCs52v
# Device #5 Private.b85 : !lw2!SA7Qp%u!+(RCbgfdm1xicVHEELHdD!V24je


def test_register_success_dup():
	'''Tests the server's REGISTER command - success and duplicate WID condition'''

	print("Test: REGISTER")
	setup_test()

	wid = '11111111-1111-1111-1111-111111111111'
	# password is 'SandstoneAgendaTricycle'
	pwhash = '$argon2id$v=19$m=65536,t=2,p=1$ew5lqHA5z38za+257DmnTA$0LWVrI2r7XCq' \
				'dcCYkJLok65qussSyhN5TTZP+OTgzEI'
	algorithm = 'curve25519'
	devkey = '@X~msiMmBq0nsNnn0%~x{M|NU_{?<Wj)cYybdh&Z'

	sock = connect()
	assert sock, "Connection to server at localhost:2001 failed"

	
	# Test #1: Regular registration that is supposed to succeed

	cmd = ' '.join([ "REGISTER", wid, pwhash, algorithm, devkey, "\r\n" ])
	print('Regular registration\n--------------------------')
	print('CLIENT: %s' % cmd)
	sock.send(cmd.encode())

	response = sock.recv(8192).decode()
	print('SERVER: %s\n' % response)
	
	parts = response.strip().split(' ')
	assert len(parts) == 3, 'Server returned wrong number of parameters'
	assert parts[0] == '201' and parts[1] == 'REGISTERED', 'Failed to register'
	assert validate_uuid(parts[2]), 'Device ID from server failed to validate'

	
	# Test #2: Attempt registration of existing WID

	cmd = ' '.join([ "REGISTER", wid, pwhash, algorithm, devkey, "\r\n" ])
	print('Duplicate registration\n--------------------------')
	print('CLIENT: %s' % cmd)
	sock.send(cmd.encode())

	response = sock.recv(8192).decode()
	print('SERVER: %s\n' % response)
	
	parts = response.split(' ')
	assert parts[0] == '408' and parts[1] == 'RESOURCE', 'Failed to catch duplicate registration'

	sock.send(b'QUIT\r\n')


def test_register_failures():
	'''Tests the server's REGISTER command with failure conditions'''

	setup_test()

	wid = '11111111-1111-1111-1111-111111111111'
	# password is 'SandstoneAgendaTricycle'
	pwhash = '$argon2id$v=19$m=65536,t=2,p=1$ew5lqHA5z38za+257DmnTA$0LWVrI2r7XCq' \
				'dcCYkJLok65qussSyhN5TTZP+OTgzEI'
	algorithm = 'curve25519'
	devkey = '@X~msiMmBq0nsNnn0%~x{M|NU_{?<Wj)cYybdh&Z'

	sock = connect()
	assert sock, "Connection to server at localhost:2001 failed"

	
	# Test #1: Attempt registration with unsupported encryption type

	wid = '11111111-1111-1111-1111-222222222222'
	cmd = ' '.join([ "REGISTER", wid, pwhash, '3DES', devkey, "\r\n" ])
	print('Bad encryption algorithm\n--------------------------')
	print('CLIENT: %s' % cmd)
	sock.send(cmd.encode())

	response = sock.recv(8192).decode()
	print('SERVER: %s' % response)
	
	parts = response.split(' ')
	assert parts[0] == '309' and parts[1] == 'ENCRYPTION', 'Failed to catch unsupported algorithm'


	# Test #2: Send bad WID

	wid = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
	cmd = ' '.join([ "REGISTER", wid, pwhash, algorithm, devkey, "\r\n" ])
	print('Bad WID\n--------------------------')
	print('CLIENT: %s' % cmd)
	sock.send(cmd.encode())

	response = sock.recv(8192).decode()
	print('SERVER: %s' % response)
	
	parts = response.split(' ')
	assert parts[0] == '400' and parts[1] == 'BAD', 'Failed to catch bad WID'

	sock.send(b'QUIT\r\n')


def test_overflow():
	'''Tests the server's command handling for commands greater than 8K'''

	print("Test: Command Overflow")
	setup_test()

	wid = '11111111-1111-1111-1111-111111111111'
	# password is 'SandstoneAgendaTricycle'
	pwhash = '$argon2id$v=19$m=65536,t=2,p=1$ew5lqHA5z38za+257DmnTA$0LWVrI2r7XCq' \
				'dcCYkJLok65qussSyhN5TTZP+OTgzEI'
	algorithm = 'curve25519'
	devkey = '@X~msiMmBq0nsNnn0%~x{M|NU_{?<Wj)cYybdh&Z'

	sock = connect()
	assert sock, "Connection to server at localhost:2001 failed"

	wid = 'A' * 10240
	cmd = ' '.join([ "REGISTER", wid, pwhash, algorithm, devkey, "\r\n" ])
	print('Overflow\n--------------------------')
	print('CLIENT: %s' % cmd)
	sock.send(cmd.encode())

	response = sock.recv(8192).decode()
	print('SERVER: %s' % response)
	
	parts = response.split(' ')
	assert parts[0] == '400' and parts[1] == 'BAD', 'Failed to catch overflow'


	sock.send(b'QUIT\r\n')


if __name__ == '__main__':
	test_register_success_dup()
	test_register_failures()
	test_overflow()