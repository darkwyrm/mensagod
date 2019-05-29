import clientlib as clib
from errorcodes import ERR_OK, ERR_BAD_DATA, ERR_CUSTOM, ERR_UNKNOWN_COMMAND
from shellbase import BaseCommand, gShellCommands

import collections
from glob import glob
import os
import platform
import socket
import subprocess
import sys

from prompt_toolkit import print_formatted_text, HTML

# Special class for handling blanks
class CommandEmpty(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,rawInput,pTokenList)
		self.name = ''


# Special class for handling anything the shell doesn't support
class CommandUnrecognized(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,'unrecognized')
		self.name = 'unrecognized'

	def IsValid(self):
		return ERR_UNKNOWN_COMMAND

	def Execute(self, pShellState):
		return ERR_UNKNOWN_COMMAND


class CommandChDir(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,rawInput,pTokenList)
		self.name = 'chdir'
		self.helpInfo = 'Usage: cd <location>\nChanges to the specified directory\n\n' + \
						'Aliases: cd'
		self.description = 'change directory/location'

	def GetAliases(self):
		return { "cd":"chdir" }

	def Execute(self, pShellState):
		if (len(self.tokenList) > 0):
			newDir = ''
			if '~' in self.tokenList[0]:
				if platform.system().casefold() == 'windows':
					newDir = self.tokenList[0].replace('~', os.getenv('USERPROFILE'))
				else:
					newDir = self.tokenList[0].replace('~', os.getenv('HOME'))
			else:
				newDir = self.tokenList[0]
			try:
				os.chdir(newDir)
			except Exception as e:
				ERR_CUSTOM.string = e.__str__()
				return ERR_CUSTOM

		pShellState.oldpwd = pShellState.pwd
		pShellState.pwd = os.getcwd()

		return ERR_OK

	def Autocomplete(self, pTokens):
		if len(pTokens) == 1:
			outData = list()
			
			if pTokens[0][0] == '"':
				quoteMode = True
			else:
				quoteMode = False
			
			if quoteMode:
				items = glob(pTokens[0][1:] + '*')
			else:
				items = glob(pTokens[0] + '*')
			
			for item in items:
				if not os.path.isdir(item):
					continue

				display = item
				if quoteMode or ' ' in item:
					data = '"' + item + '"'
				else:
					data = item
				outData.append([data,display])
					
			return outData
		else:
			return list()


class CommandListDir(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,rawInput,pTokenList)
		self.name = 'ls'
		self.helpInfo = 'Usage: as per bash ls command or Windows dir command'
		self.description = 'list directory contents'

	def GetAliases(self):
		return { "dir":"ls" }

	def Execute(self, pShellState):
		if sys.platform == 'win32':
			tokens = ['dir','/w']
			tokens.extend(self.tokenList)
			subprocess.call(tokens, shell=True)
		else:
			tokens = ['ls','--color=auto']
			tokens.extend(self.tokenList)
			subprocess.call(tokens)
		return ERR_OK

	def Autocomplete(self, pTokens):
		if len(pTokens) == 1:
			outData = list()
			
			if pTokens[0][0] == '"':
				quoteMode = True
			else:
				quoteMode = False
			
			if quoteMode:
				items = glob(pTokens[0][1:] + '*')
			else:
				items = glob(pTokens[0] + '*')
			
			for item in items:
				if not os.path.isdir(item):
					continue

				display = item
				if quoteMode or ' ' in item:
					data = '"' + item + '"'
				else:
					data = item
				outData.append([data,display])
					
			return outData
		else:
			return list()


class CommandExit(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,'exit')
		self.name = 'exit'
		self.helpInfo = 'Usage: exit\nCloses the connection and exits the shell.'
		self.description = 'Exits the shell'

	def GetAliases(self):
		return { "x":"exit", "q":"exit", "exit":"exit" }

	def Execute(self, shellState):
		sys.exit(0)


class CommandHelp(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,'help')
		self.name = 'help'
		self.helpInfo = 'Usage: help <command>\nProvides information on a command.\n\n' + \
						'Aliases: ?'
		self.description = 'Show help on a command'

	def GetAliases(self):
		return { "?":"help" }

	def Execute(self, shellState):
		if (len(self.tokenList) > 0):
			# help <keyword>
			for cmdName in self.tokenList:
				if (len(cmdName) < 1):
					continue

				if (cmdName in gShellCommands):
					print(gShellCommands[cmdName].GetHelp())
				else:
					print_formatted_text(HTML(
						"No help on <gray><b>%s</b></gray>" % cmdName))
		else:
			# Bare help command: print available commands
			ordered = collections.OrderedDict(sorted(gShellCommands.items()))
			for name,item in ordered.items():
				print_formatted_text(HTML(
					"<gray><b>%s</b>\t%s</gray>" % (name, item.GetDescription())
				))
		return ERR_OK


class CommandShell(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self,rawInput,pTokenList)
		self.name = 'shell'
		self.helpInfo = 'Usage: shell <command>\n' + \
						'Executes a command directly in the regular user shell.\n' + \
						'Aliases: ` , sh'
		self.description = 'Run a shell command'

	def GetAliases(self):
		return { "sh":"shell", "`":"shell" }

	def Execute(self, pShellState):
		os.system(' '.join(self.tokenList))
		return ERR_OK


class CommandConnect(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self, rawInput, pTokenList)
		self.name = 'connect'
		self.helpInfo = 'Usage: connect <host>\n' + \
						'Open a connection to a host on port 2001.\n' + \
						'Aliases: con'
		self.description = 'Connect to a host'

	def GetAliases(self):
		return { "con":"connect" }

	def Execute(self, pShellState):
		if (len(self.tokenList) != 1):
			print(self.helpInfo)
			return ERR_OK

		if pShellState.socket:
			clib.quit(pShellState.sock)
			pShellState.sock = None
		
		out_data = clib.connect(self.tokenList[0])
		if out_data['error'] == ERR_OK:
			pShellState.sock = out_data['socket']
			if out_data['version']:
				print("Connected to %s, version %s" % (self.tokenList[0], \
														out_data['version']))
			else:
				print('Connected to host')
		else:
			print(out_data['errorstring'])

		return out_data['error']


class CommandDisconnect(BaseCommand):
	def __init__(self, rawInput=None, pTokenList=None):
		BaseCommand.Set(self, rawInput, pTokenList)
		self.name = 'disconnect'
		self.helpInfo = 'Usage: disconnect <host>\n' + \
						'Close the server connection'
		self.description = 'Disconnect from the host'

	def Execute(self, pShellState):
		clib.quit(pShellState.sock)

		return ERR_OK

