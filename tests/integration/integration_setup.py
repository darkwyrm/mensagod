import os.path
import sys

import psycopg2
import toml

def LoadDBConfig(config_file_path: str) -> dict:
	'''Loads the Anselus server configuration from the config file'''
	if os.path.exists(config_file_path):
		try:
			serverconfig = toml.load(config_file_path)
		except Exception as e:
			print("Unable to load server config %s: %s" % (config_file_path, e))
			sys.exit(1)
	else:
		serverconfig = {}

	serverconfig.setdefault('database', dict())
	serverconfig['database'].setdefault('engine','postgresql')
	serverconfig['database'].setdefault('ip','127.0.0.1')
	serverconfig['database'].setdefault('port','5432')
	serverconfig['database'].setdefault('name','anselus')
	serverconfig['database'].setdefault('user','anselus')
	serverconfig['database'].setdefault('password','CHANGEME')

	serverconfig.setdefault('network', dict())
	serverconfig['network'].setdefault('listen_ip','127.0.0.1')
	serverconfig['network'].setdefault('port','2001')

	serverconfig.setdefault('global', dict())
	serverconfig['global'].setdefault('workspace_dir','/var/anselus')
	serverconfig['global'].setdefault('registration','private')
	serverconfig['global'].setdefault('default_quota',0)

	serverconfig.setdefault('security', dict())
	serverconfig['security'].setdefault('failure_delay_sec',3)
	serverconfig['security'].setdefault('max_failures',5)
	serverconfig['security'].setdefault('lockout_delay_min',15)
	serverconfig['security'].setdefault('registration_delay_min',15)

	if serverconfig['database']['engine'].lower() != 'postgresql':
		print("This script exepects a server config using PostgreSQL. Exiting")
		sys.exit()
	
	return serverconfig


def DBSetup(serverconfig: dict, schema_path: str):
	'''Reset the test database to defaults'''
	try:
		conn = psycopg2.connect(host=serverconfig['database']['ip'],
								port=serverconfig['database']['port'],
								database="anselus_test",
								user=serverconfig['database']['user'],
								password=serverconfig['database']['password'])
	except Exception as e:
		print("Couldn't connect to database: %s" % e)
		sys.exit(1)

	# TODO: Load schema from file

	cur = conn.cursor()

	# TODO: Run schema setup script

	cur.close()
	conn.commit()
