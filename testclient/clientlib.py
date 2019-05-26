# This file contains the functions needed by any Anselus client for 
# communications and map pretty much 1-to-1 to the commands outlined in the
# spec

from errorcodes import ERR_OK, ERR_CONNECTION, ERR_NO_SOCKET, \
						ERR_HOST_NOT_FOUND

import socket

# Number of seconds to wait for a client before timing out
CONN_TIMEOUT = 900.0

# Size (in bytes) of the read buffer size for recv()
READ_BUFFER_SIZE = 8192

# Read
#	Requires: valid socket
#	Returns: string
def read_text(sock):
	try:
		out = sock.recv(READ_BUFFER_SIZE)
	except:
		sock.close()
		return None
	
	try:
		out_string = out.decode()
	except:
		return ''
	
	return out_string


# Connect
#	Requires: host (hostname or IP)
#	Optional: port number
#	Returns: [dict]	socket
#					error code
#					error string
#					
def connect(host, port=2001):
	out_data = dict()
	try:
		sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
		# Set a short timeout in case the server doesn't respond immediately,
		# which is the expectation as soon as a client connects.
		sock.settimeout(10.0)
	except:
		out_data['socket'] = None
		out_data['error'] = ERR_NO_SOCKET
		out_data['error_string'] = "Couldn't create a socket"
		return out_data
	out_data['socket'] = sock

	try:
		host_ip = socket.gethostbyname(host)
	except socket.gaierror:
		sock.close()
		out_data['socket'] = None
		out_data['error'] = ERR_HOST_NOT_FOUND
		out_data['error_string'] = "Couldn't locate host %s" % host
		return out_data
	
	out_data['ip'] = host_ip
	try:
		sock.connect((host_ip, port))
		out_data['error'] = ERR_OK
		out_data['error_string'] = "OK"
		
		hello = read_text(sock)
		if hello:
			hello = hello.strip().split()
			if len(hello) >= 3:
				out_data['version'] = hello[2]
			else:
				out_data['version'] = ''

	except Exception as e:
		sock.close()
		out_data['socket'] = None
		out_data['error'] = ERR_CONNECTION
		out_data['error_string'] = "Couldn't connect to host %s: %s" % (host, e)

	# Set a timeout of 15 minutes
	sock.settimeout(900.0)
	return out_data
	
# Quit
#	Requires: socket
#	Returns: nothing
def quit(sock):
	if (sock):
		try:
			sock.send('QUIT\r\n')
		except:
			pass