import log
from serverconfig import gConfig
from workspace import Workspace

import os
import os.path as path
import time

def send_string(sock, s):
	# TODO: Implement -- be sure to check for max message size
	# and throw an exception if exceeded
	pass

def receive_string(sock, s):
	# TODO: Implement -- accept no more than 8000 characters
	pass

class BaseCommand:
	def __init__(self, pTokens=None, sock=None):
		self.Set(pTokens, sock)
		
	def Set(self, pTokens, sock):
		self.rawTokens = pTokens
		self.socket = sock
	
	def IsValid(self):
		# Subclasses validate their information and return an error object
		return True
	
	def Execute(self, pExtraData):
		# The base class purposely does nothing. To be implemented by subclasses
		return False

# Create Workspace
# ADDWKSPC
# Parameters: None
# Success Returns:
#   1) mailbox identifier
#	2) device ID to be used for the current device
#	3) user quota size
# 
# Safeguards: if the IP address of requester is not localhost, wait a
#	configurable number of seconds to prevent spamming / DoS.
class CreateWorkspaceCommand(BaseCommand):
	def Execute(self, pExtraData):
		# If the mailbox creation request has been made from outside the
		# server, check to see if it has been made more recently than the
		# timeout set in the server configuration file.
		if pExtraData['host']:
			safeguard_path = path.join(gConfig['safeguardsdir'],
											pExtraData['host'])
			if pExtraData['host'] != '127.0.0.1' and path.exists(safeguard_path):
				time_diff = int(time.time() - path.getmtime(safeguard_path))
				if time_diff < \
						gConfig['account_timeout']:
					err_msg = ' '.join(["-ERR Please wait ", str(time_diff), \
										"seconds to create another account.\r\n"])
					send_string(self.socket, err_msg)
					return False

			with open(safeguard_path, 'a'):
				os.utime(safeguard_path)
		else:
			# It's a bug to have this missing
			raise ValueError('Missing host in CreateWorkspace')
		
		new_workspace = Workspace()
		new_workspace.generate()
		if not new_workspace.ensure_directories() or not new_workspace.save():
			send_string(self.socket, '-ERR Internal error. Sorry!\r\n')
			return False
		
		device_id = new_workspace.devices.keys()[0]
		session_id = new_workspace.devices[device_id]
		send_string(self.socket, "+OK %s %s %s\r\n.\r\n" % (new_workspace.id, device_id, session_id))
		

# Delete Workspace
# DELWKSPC
# Parameters:
#	1) Required: ID of the workspace to delete
#   2) Required: public key to be used for incoming mail for the workspace
#	3) Required: password for the account encrypted with said public key
# Success Returns:
#   1) mailbox identifier
#	2) device ID to be used for the current device
# 
# Safeguards: if the IP address of requester is not localhost, wait a
#	configurable number of seconds to prevent a mass delete attack.
class DeleteWorkspaceCommand(BaseCommand):
	# TODO: Implement DeleteWorkspaceCommand
	pass


# Check path exists
# EXISTS
# Parameters:
#	1) Required: ID of the workspace
#	2) Required: 1 or more words denoting the entire path
# Success Returns:
#	1) +OK
# Error Returns:
#	1) -ERR
#
# Safeguards: if a path isn't supplied -- only the workspace ID -- the command
# automatically fails.
class ExistsCommand(BaseCommand):
	def IsValid(self):
		if len(self.rawTokens) < 2:
			send_string(self.socket, "-ERR\r\n")
			return True
		
		if not path.exists(path.join(gConfig['workspacedir'], self.rawTokens[0])):
			send_string(self.socket, "-ERR\r\n")
			return True

		return True
	
	def Execute(self, pExtraData):
		# TODO: join relative path once we have the workspace path,
		# which is needed for this function.
		try:
			full_path = path.exists(path.join(gConfig['workspacedir'],
												self.rawTokens))
		except:
			# If it explodes, it's automatically invalid
			send_string(self.socket, "-ERR\r\n")
			return True
		
		if os.path.exists(full_path):
			send_string(self.socket, "+OK\r\n")
		else:
			send_string(self.socket, "-ERR\r\n")
		return True


# Tasks to implement commands for
# Add user
# Delete user
# Add device
# Remove device
# Store item
# Download item
# Send item
# Get new items

gCommandMap = {
	'addwkspc' : CreateWorkspaceCommand,
	'delwkspc' : DeleteWorkspaceCommand,
	'exists' : ExistsCommand
}

def handle_command(pTokens, conn, host):
	if not pTokens:
		return True
	
	verb = pTokens[0].casefold()
	if verb == 'quit':
		log.Log("Closing connection to %s" % str(host), log.INFO)
		conn.close()
		return False
	
	log.Log("Received command: %s" % ' '.join(pTokens), log.DEBUG)
	if verb in gCommandMap:
		extraData = {
			'host': host,
			'connection' : conn
		}
		cmdfunc = gCommandMap[verb]
		cmdobj = cmdfunc(pTokens, conn)
		if cmdobj.IsValid():
			cmdobj.Execute(extraData)
		else:
			send_string(conn, '-ERR Invalid command\r\n')

	return True
