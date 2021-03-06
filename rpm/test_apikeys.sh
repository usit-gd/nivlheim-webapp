#!/bin/bash

if [[ "$1" == "--setup" ]]; then
	sudo sed -i 's/AuthRequired=.*/AuthRequired=yes/' /etc/nivlheim/server.conf
	sudo systemctl restart nivlheim
	sleep 5
	# Read database connection options from server.conf and set ENV vars for psql
	grep -ie "^pg" /etc/nivlheim/server.conf | sed -e 's/\(.*\)=/\U\1=/' > /tmp/dbconf
	source /tmp/dbconf
	rm /tmp/dbconf
	export PGHOST PGPORT PGDATABASE PGUSER PGPASSWORD
	# Create an API key (Can't do this through the API because, duh, I don't have an API key)
	cd /tmp
	PGOPTIONS='--client-min-messages=warning' \
		psql -X -q -c "TRUNCATE TABLE apikeys RESTART IDENTITY CASCADE;INSERT INTO apikeys(key,ownergroup) VALUES('abcd','CI');"
	A=$(echo $SSH_CLIENT | awk '{print $1}')
	psql -X -q -c "INSERT INTO apikey_ips(keyid,iprange) VALUES(1,'$A/32');"
	exit
fi

echo "------------- Testing API keys ------------"

USER=$1
IP=$2

scp -o StrictHostKeyChecking=no \
	-q -o UserKnownHostsFile=/dev/null \
	$0 $USER\@$IP:

ssh $USER\@$IP -o StrictHostKeyChecking=no \
	-q -o UserKnownHostsFile=/dev/null \
	-C "chmod 777 ./test_apikeys.sh; ./test_apikeys.sh --setup"

# this command should give http status 401
status=$(curl -ksS -o /tmp/output -w "%{http_code}" -H "Authorization: APIKEY asldjasldfjk" "https://$IP/api/v2/hostlist?fields=hostname")
if [[ "$status" -ne "401" ]]; then
	echo "Authorization: APIKEY asldjasldfjk"
	echo "Status: $status"
	cat /tmp/output
	exit 1
fi

# this command should give http status 200
status=$(curl -ksS -o /tmp/output -w "%{http_code}" -H "Authorization: APIKEY abcd" "https://$IP/api/v2/hostlist?fields=hostname")
if [[ "$status" -ne "200" ]]; then
	echo "Authorization: APIKEY abcd"
	echo "Status: $status"
	cat /tmp/output
	exit 1
fi

echo "Test result: OK"
