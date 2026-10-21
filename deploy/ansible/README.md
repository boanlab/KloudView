# Ansible

Playbooks that deploy the KloudView server and its agents, and take them through
upgrades. The manual — topology, what each playbook does, secrets, first
deployment, upgrades and migrations — is
[docs/ansible-deployment.md](../../docs/ansible-deployment.md)
([한국어](../../docs/ansible-deployment.ko.md)).

```bash
make verify-agent-binaries VERSION=x.y.z   # repository root
cd deploy/ansible
cp inventory/hosts.example.yml inventory/hosts.yml   # then edit
./gen-vault.sh                                       # secrets, once
ansible-playbook server.yml
ansible-playbook agents.yml
```

`inventory/hosts.yml` and `inventory/group_vars/all/vault.yml` describe one
deployment and stay out of this repository. The vault is opened by
`~/.ansible/kloudview-vault-pass`, kept outside it. Back all three up: the
secrets cannot be regenerated on a running deployment.
