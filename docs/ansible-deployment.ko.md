# Ansible로 배포하기

[English](ansible-deployment.md)

[`deploy/ansible`](../deploy/ansible)의 플레이북은 서버를 한 대의 호스트에,
에이전트를 관리 대상 호스트마다 설치합니다. 이후 업그레이드도 같은 플레이북으로
진행합니다. 레포에 이미 있는 배포 수단을 다시 구현하지 않고 그대로 호출합니다.
서버는 `docker-compose.yml`, 에이전트는 서버가 제공하는 설치 스크립트를 씁니다.
그래서 이 문서의 절차는 [deployment.md](deployment.md)와
[agent-installation.md](agent-installation.md)에 적힌 내용을 한곳에서 실행하는
것과 같습니다.

## 1. 토폴로지

```text
 컨트롤 노드 (운영자 작업 PC)
 ┌───────────────────────────────────────┐
 │ 이 레포의 git checkout                │
 │ deploy/ansible   플레이북과 role      │
 │ dist/            에이전트 빌드        │
 │ ~/.ansible/kloudview-vault-pass       │
 └──────────────┬────────────────────────┘
                │ SSH (Ansible)
       ┌────────┴──────────────────────────────────────┐
       ▼                                               ▼
 서버 호스트 (kloudview_server)                  관리 대상 호스트 (kloudview_agents)
 ┌───────────────────────────────────────┐      ┌──────────────────────────────┐
 │ TLS 리버스 프록시 :443  (직접 구성)   │◀─────│ kloudview-agent (systemd,    │
 │        │ X-Forwarded-Proto/Host       │ HTTPS│ 비특권 kloudview 계정)       │
 │        ▼                              │ 아웃 │                              │
 │ Docker Compose 프로젝트 "kloudview"   │ 바운드│ 서버의                       │
 │ ┌───────────────┐   ┌───────────────┐ │      │ /api/v1/agent-install.sh로   │
 │ │ server :8080  │──▶│ postgres 17   │ │      │ 설치                         │
 │ │ 콘솔 + API    │   │ 내부 전용     │ │      └──────────────────────────────┘
 │ └───────────────┘   └───────────────┘ │
 └───────────────────────────────────────┘
```

- Ansible은 SSH로 각 머신에 접속합니다. 관리 대상 호스트가 컨트롤 노드로 다시
  접속하는 일은 없습니다.
- 에이전트는 서버의 공개 주소로 **아웃바운드** 연결을 엽니다. 설치 스크립트,
  바이너리, 이후 업데이트를 모두 서버에서 받고, 서버에 보고합니다.
- PostgreSQL은 Compose 네트워크 안에서만 접근할 수 있습니다.
- TLS 프록시는 플레이북에 **포함되어 있지 않습니다.** 서버 호스트의 8080 포트(`kloudview_server_http_port`)가
  열리므로 그 앞에 `X-Forwarded-Proto`와 `X-Forwarded-Host` 헤더를 넘기는 프록시를
  두세요([이유](deployment.md#behind-a-tls-ingress)).

### 무엇이 어디에 놓이는가

| 머신 | 경로 | 내용 |
|---|---|---|
| 서버 | `/opt/kloudview/src/` | 배포된 커밋. `git archive`로 추출한 것 |
| 서버 | `/opt/kloudview/src/.env` | 시크릿과 설정. vault에서 생성하며 권한 `0600` |
| 서버 | `/opt/kloudview/src/dist/` | 에이전트에게 제공할 빌드. 읽기 전용으로 마운트됨 |
| 서버 | 볼륨 `kloudview_kloudview-postgres-data`, `kloudview_kloudview-data` | DB와 서버 상태. 재배포해도 유지됨 |
| 에이전트 | `/var/lib/kloudview/bin/kloudview-agent` | 바이너리 |
| 에이전트 | `/var/lib/kloudview/agent.json` | 등록된 호스트 identity와 credential |
| 에이전트 | `/etc/kloudview/agent.env`, `/etc/systemd/system/kloudview-agent.service` | 설치 스크립트가 쓴 설정과 유닛 |

## 2. 플레이북이 하는 일

| 플레이북 | 대상 |
|---|---|
| `server.yml` | `kloudview_server` |
| `agents.yml` | `kloudview_agents` |
| `site.yml` | 둘 다. 서버 먼저 |

**`server.yml`**(role `kloudview_server`)은 다음 순서로 진행합니다.

1. **사전 점검.** 시크릿이 서버 규칙을 만족하는지 확인합니다. 규칙은 토큰 16자
   이상, credential key 32자 이상, 두 값이 서로 다름입니다. `dist/`에 에이전트
   빌드가 있는지도 확인합니다. 빌드가 없으면 설치 스크립트가 `503`을 반환하기
   때문입니다. 목표 에이전트 버전을 지정했다면 `dist/VERSION`과 일치하는지도
   확인합니다.
2. **Docker 설치.** 배포판 패키지(Ubuntu/Debian)로 `docker.io`, `docker-compose-v2`,
   `rsync`를 설치합니다. 건너뛰려면 `kloudview_server_install_docker: false`로
   설정합니다.
3. **소스 동기화.** 컨트롤 노드 checkout에서 `kloudview_server_git_ref`(기본 `HEAD`)를
   `git archive`로 추출해 `/opt/kloudview/src`로 rsync합니다. 커밋하지 않은 변경과
   추적하지 않는 파일은 배포되지 않습니다. 서버의 `.env`와 `dist/`는 동기화 대상에서
   제외됩니다.
4. **에이전트 빌드 동기화.** `dist/`를 서버로 rsync합니다.
5. **`.env` 생성.** vault와 group 변수로 `templates/env.j2`를 렌더링합니다.
6. **Compose 실행.** 프로젝트 `kloudview`로 `docker compose up`을 실행합니다. 서버
   이미지는 소스가 바뀐 경우에만 다시 빌드합니다. 설정이 바뀐 컨테이너는 Compose가
   다시 만들기 때문에 `.env` 변경도 반영됩니다. 빌드 과정에서 서버 테스트가 돌기
   때문에 깨진 커밋은 이 단계에서 실패합니다.
7. **확인.** `/healthz`가 응답할 때까지 기다린 뒤, 서버가 시작할 때 남긴
   `not production hardened` 경고가 있으면 출력합니다.

**`agents.yml`**(role `kloudview_agent`)은 다음 순서로 진행합니다.

1. 두 호스트의 호스트명이 같으면 중단합니다. 에이전트 identity가 호스트명에서
   만들어지기 때문입니다.
2. `curl`을 설치하고 `<public_url>/api/v1/agent-install.sh`를 내려받습니다.
3. 등록 토큰을 환경변수로 넘겨 스크립트를 실행합니다. 그래서 토큰이 프로세스
   목록이나 Ansible 출력에 남지 않습니다. 이미 `/var/lib/kloudview/agent.json`이
   있는 호스트는 건너뜁니다. 다시 설치하려면 `kloudview_agent_reinstall=true`를
   지정합니다.
4. `kloudview-agent`가 실행 중이고 enable 상태인지 확인합니다.

`kloudview_agent_state=absent`를 지정하면 설치 대신 서버의 제거 스크립트를
실행합니다.

모든 실행은 멱등입니다. 바뀐 게 없으면 다시 실행해도 `changed=0`입니다.

## 3. 시크릿

`deploy/ansible/inventory/group_vars/all/vault.yml`에는 네 가지 시크릿이
`ansible-vault`로 암호화되어 들어 있습니다. 등록 토큰, 에이전트 credential key,
PostgreSQL 비밀번호, 초기 admin 비밀번호입니다.

이 파일을 여는 키는 `~/.ansible/kloudview-vault-pass`이고,
`deploy/ansible/ansible.cfg`가 이 경로를 가리킵니다. 레포 밖에 있으니 커밋될 수
없습니다. 다른 운영자에게는 비밀번호 관리자로 전달하세요. 레포나 채팅으로 보내면
안 됩니다.

**vault 파일과 `inventory/hosts.yml`은 이 레포에 커밋하지 않습니다.** 두 파일은
특정 배포 하나의 시크릿과 주소를 담고 있고, 이 레포는 모든 배포가 출발하는
프로젝트입니다. 여기에 vault를 커밋하면 다른 사람은 아무도 열 수 없는 파일이
되고, 다른 배포는 자기 vault를 만들 때마다 추적 중인 파일을 덮어써야 합니다.
배포별 `vault.yml`과 `hosts.yml`은 별도의 비공개 레포에 두거나, 비밀번호 파일과
함께 백업하세요.

**비밀번호 파일은 반드시 백업하세요.** 이 파일이 없으면 vault를 열 수 없고, 안의
값을 새로 만들어 대체할 수도 없습니다.

- DB는 처음 초기화할 때의 비밀번호를 계속 유지합니다.
- 이미 등록된 에이전트는 credential key에 묶인 credential을 갖고 있습니다.
- admin 비밀번호는 서버가 처음 시작할 때 한 번만 설정됩니다.

두 파일이 모두 없는 컨트롤 노드에서 새로 만들려면 다음을 실행합니다.

```bash
deploy/ansible/gen-vault.sh
```

두 파일 중 하나라도 이미 있으면 `--force` 없이는 덮어쓰지 않습니다. 운영 중인
배포의 시크릿을 바꾸면 DB와 등록된 모든 에이전트가 접속하지 못하게 됩니다.

**컨트롤 노드를 다른 머신으로 옮길 때**는 두 파일을 모두 옮겨야 합니다.
`vault.yml`은 레포와 함께 따라가지만, 비밀번호 파일은 따라가지 않습니다. 게다가
Ansible은 이 파일을 실행하는 사용자의 홈 디렉터리에서 찾습니다. 파일이 없으면
모든 명령이 `The vault password file … was not found`로 멈춥니다.

```bash
# 새 컨트롤 노드에서
mkdir -p ~/.ansible && chmod 700 ~/.ansible
scp <기존-컨트롤-노드>:.ansible/kloudview-vault-pass ~/.ansible/
chmod 600 ~/.ansible/kloudview-vault-pass
ansible-vault view deploy/ansible/inventory/group_vars/all/vault.yml >/dev/null && echo ok
```

```bash
cd deploy/ansible
ansible-vault view inventory/group_vars/all/vault.yml   # 예: 초기 admin 비밀번호 확인
ansible-vault edit inventory/group_vars/all/vault.yml
```

## 4. 최초 배포

### 4.1 컨트롤 노드 준비

```bash
python3 -m venv ~/.venvs/ansible
~/.venvs/ansible/bin/pip install ansible-core
export PATH=~/.venvs/ansible/bin:$PATH
ansible-galaxy collection install community.docker community.general ansible.posix
```

이 밖에 `git`, `rsync`, 그리고 에이전트 바이너리를 빌드할 Docker가 필요합니다.

### 4.2 인벤토리와 설정

- `deploy/ansible/inventory/hosts.yml`: `hosts.example.yml`을 복사해 만듭니다. 예시 파일에
  각 이름의 의미와 `--limit` 사용법이 설명되어 있습니다. 서버는 `kloudview_server`에, 관리 대상
  호스트는 `kloudview_agents`에 넣습니다.
- `deploy/ansible/inventory/group_vars/all/main.yml`: `kloudview_public_url`에
  운영자와 에이전트가 서버에 접속할 `https://` 주소를 씁니다.
- 필요하면 같은 파일이나 `group_vars/kloudview_server.yml`에서 아래 변수를
  덮어씁니다.

| 변수 | 기본값 | 의미 |
|---|---|---|
| `kloudview_server_git_ref` | `HEAD` | 배포할 커밋, 태그 또는 브랜치 |
| `kloudview_server_version` | `dev` | `/healthz`가 보고하는 버전 |
| `kloudview_server_agent_target_version` | 비어 있음 | fleet이 실행할 에이전트 버전. 비어 있으면 업데이트를 알리지 않음 |
| `kloudview_server_agent_canary` | 비어 있음 | 새 에이전트 빌드를 가장 먼저 받을 호스트명 |
| `kloudview_server_agent_canary_soak` | `10m` | 나머지가 따라오기 전에 canary가 빌드를 유지해야 하는 시간 |
| `kloudview_server_install_dir` | `/opt/kloudview` | 서버의 설치 위치 |
| `kloudview_server_http_port` | `8080` | 서버를 공개할 호스트 포트. 같은 호스트의 프록시만 접근하게 하려면 `127.0.0.1:8080` |
| `kloudview_agent_options` | `[]` | 설치 스크립트 옵션: `--no-logs`, `--no-containers`, `--no-vms`, `--no-terminal`, `--no-auto-update` |

설치 스크립트는 `--no-terminal`을 주지 않으면 승인 기반 원격 셸을 켭니다.

### 4.3 배포

```bash
make verify-agent-binaries VERSION=0.1.0          # 레포 루트에서 실행: dist/ 생성

cd deploy/ansible
./gen-vault.sh                                    # 한 번만: 비밀번호 파일과 vault 생성 (3장)
ansible all -m ping                               # 모든 호스트에 SSH와 sudo가 되는지 확인
ansible-playbook server.yml -e kloudview_server_version=0.1.0
ansible-playbook agents.yml --limit node01.example.com   # 먼저 한 대만
ansible-playbook agents.yml
```

그다음 `kloudview_public_url`에 접속해 `admin` 계정과 vault의
`vault_kloudview_admin_password`로 로그인하고 비밀번호를 바꿉니다. 혼자 운영한다면
[동료 없이 운영하기](deployment.md#working-without-a-colleague)를 참고하세요.

### 4.4 호스트 추가와 제거

```bash
# hosts.yml의 kloudview_agents에 호스트를 추가한 뒤
ansible-playbook agents.yml --limit node03.example.com

# 호스트에서 에이전트 제거 (콘솔에서 따로 지우기 전까지 노드 기록은 남음)
ansible-playbook agents.yml --limit node03.example.com -e kloudview_agent_state=absent
```

## 5. 업그레이드와 마이그레이션

### 5.1 서버가 DB를 마이그레이션하는 방식

스키마 마이그레이션은 서버 바이너리에 포함되어 있습니다. 서버는 시작할 때마다
PostgreSQL advisory lock을 잡고, `kloudview_schema_migrations`에 기록된 버전보다
새로운 마이그레이션을 **하나의 트랜잭션으로 모두** 적용합니다. 그래서 다음이
성립합니다.

- 스키마 업그레이드를 위한 별도 단계가 없습니다. 새 서버가 시작하면서 수행합니다.
- 마이그레이션이 실패하면 전부 롤백되고 서버가 시작하지 않습니다. 이때
  `Start PostgreSQL and the server` 태스크가 실패하고, DB는 바뀌지 않은 상태로
  남습니다.
- 마이그레이션은 앞으로만 진행하며 down 마이그레이션이 없습니다. 구버전 서버를
  신버전 스키마 위에서 띄워도 거부하지 않고, 모르는 테이블 위에서 그대로
  동작합니다. 따라서 **마이그레이션이 포함된 버전을 되돌리려면 이전 커밋을 다시
  배포하는 것만으로는 부족하고, 업그레이드 전에 받은 백업을 복원해야 합니다.**

### 5.2 먼저 릴리스 변경 사항 확인

배포하기 전에 컨트롤 노드에서 현재 운영 중인 버전과 새로 올릴 버전을 비교합니다.

```bash
git fetch --tags
git log --oneline <배포된 ref>..<새 ref>
git diff <배포된 ref>..<새 ref> -- docker-compose.yml .env.example docs/deployment.md apps/server/internal/store/postgres.go
```

| diff에 이런 변경이 있으면 | 할 일 |
|---|---|
| `.env.example`에 새 키가 생겼거나 `docker-compose.yml`의 `environment:`에 새 항목이 생김 | `deploy/ansible/roles/kloudview_server/templates/env.j2`에 키를 추가하고, 값은 `group_vars`나 vault에 넣습니다. 템플릿은 적혀 있는 키만 쓰기 때문에 추가하지 않으면 새 키가 비어 있게 됩니다 |
| `postgresMigrations`(`apps/server/internal/store/postgres.go`)에 새 항목이 생김 | 스키마 변경입니다. 되돌릴 방법은 5.3에서 받는 백업뿐입니다 |
| `postgres:` 이미지의 **메이저** 버전이 바뀜 | 5.6을 따릅니다. 새 메이저 버전은 기존 데이터 디렉터리를 읽지 못합니다 |
| 에이전트-서버 계약(`docs/contracts/agent-server.json`)이 바뀜 | 서버를 먼저 올리고 에이전트를 올립니다(5.4). 아래 절차의 순서가 원래 이렇습니다 |

### 5.3 서버 업그레이드

```bash
cd deploy/ansible

# 1. 서버에서 DB 백업
ansible kloudview_server -b -m ansible.builtin.shell -a \
  'cd /opt/kloudview/src && docker compose -p kloudview exec -T postgres pg_dump -U kloudview -Fc kloudview > /opt/kloudview/pre-upgrade-$(date +%Y%m%d%H%M).dump'

# 2. 새 커밋 배포
ansible-playbook server.yml -e kloudview_server_git_ref=v0.2.0 -e kloudview_server_version=0.2.0

# 3. 확인
curl -s https://kloudview.example.com/healthz         # {"status":"ok","version":"0.2.0"}
```

소스가 바뀌었으므로 플레이북이 이미지를 다시 빌드하고 서버 컨테이너를 다시
만듭니다. 서버는 시작하면서 스키마를 마이그레이션합니다. PostgreSQL과 두 볼륨은
그대로입니다. 컨테이너가 재시작하는 몇 초 동안은 콘솔에 접속할 수 없습니다.

서버 디스크 자체를 따로 백업하지 않는다면 덤프 파일을 서버 밖으로 복사해 두세요.
(`ansible kloudview_server -b -m ansible.builtin.fetch -a 'src=/opt/kloudview/<파일> dest=backups/ flat=true'`)

현재 checkout된 내용이 아니라 정해진 릴리스로 배포하려면 `kloudview_server_git_ref`와
`kloudview_server_version`을 커맨드라인 대신 `group_vars`에 고정하세요. 버전을
올릴 때는 그 값을 바꾸는 커밋을 함께 만듭니다.

### 5.4 에이전트 업그레이드

먼저 컨트롤 노드에서 새 에이전트 버전을 빌드합니다.

```bash
make verify-agent-binaries VERSION=0.2.0
```

호스트에 반영하는 방법은 설치 방식에 따라 다릅니다.

**자동 업데이트(설치 스크립트 기본값).** 서버가 빌드를 배포하고, 에이전트가 직접
설치합니다.

```bash
ansible-playbook server.yml \
  -e kloudview_server_agent_target_version=0.2.0 \
  -e kloudview_server_agent_canary=node01        # 선택: 한 대 먼저
```

서버가 새 빌드와 목표 버전을 게시합니다. canary가 먼저 받고, soak 시간 동안
유지되면 나머지가 10분에 걸쳐 나눠 받습니다. 진행 상황은 콘솔의 **Agents** 페이지나
`GET /api/v1/agents/rollout`에서 볼 수 있습니다. 목표 버전이 `dist/VERSION`과 다르면
사전 점검에서 실행이 중단됩니다. 버전이 어긋난 빌드는 서버가 어떤 에이전트에도
제공하지 않기 때문입니다.

**버전 고정(`--no-auto-update`).** Ansible이 버전을 관리합니다.

```bash
ansible-playbook server.yml                                 # 새 빌드를 서버에 게시
ansible-playbook agents.yml -e kloudview_agent_reinstall=true --limit node01
ansible-playbook agents.yml -e kloudview_agent_reinstall=true
```

재설치해도 호스트의 identity와 이력은 유지됩니다.

이미 등록된 호스트의 `kloudview_agent_options`를 바꿀 때도
`kloudview_agent_reinstall=true`가 필요합니다.

### 5.5 롤백

두 커밋 사이에 마이그레이션이 추가되지 않았다면 이전 커밋을 다시 배포하는 것으로
충분합니다.

```bash
ansible-playbook server.yml -e kloudview_server_git_ref=v0.1.0 -e kloudview_server_version=0.1.0
```

마이그레이션이 추가됐다면 업그레이드 전에 받은 덤프도 서버 호스트에서 복원해야
합니다.

```bash
cd /opt/kloudview/src
P="docker compose -p kloudview"
$P stop server
$P exec -T postgres dropdb   -U kloudview kloudview
$P exec -T postgres createdb -U kloudview kloudview
$P exec -T postgres pg_restore -U kloudview -d kloudview --no-owner < /opt/kloudview/pre-upgrade-<stamp>.dump
```

그다음 이전 ref로 `server.yml`을 실행하면 서버가 다시 시작됩니다. 백업 이후에
기록된 데이터는 사라집니다.

에이전트는 업그레이드할 때와 같은 방식으로 되돌립니다. 자동 업데이트라면 목표
버전을 이전 값으로 돌리고, 버전 고정이라면 이전 `dist/`로 다시 설치합니다.

### 5.6 PostgreSQL 메이저 버전 업그레이드

`docker-compose.yml`에서 `postgres:`의 메이저 버전이 바뀌면(예: 17 → 18) 새
PostgreSQL이 기존 데이터 디렉터리를 읽지 못합니다. 덤프하고, 새로 만들고,
복원합니다.

```bash
# 서버 호스트에서, 아직 기존 이미지로 실행 중일 때
cd /opt/kloudview/src
docker compose -p kloudview exec -T postgres pg_dump -U kloudview -Fc kloudview > /opt/kloudview/pg-major.dump
docker compose -p kloudview down                                  # 볼륨은 유지됨
docker volume rm kloudview_kloudview-postgres-data                # 덤프를 검증한 뒤에만 실행

# 컨트롤 노드에서: 새 compose 파일을 배포하고 빈 DB로 시작
ansible-playbook server.yml -e kloudview_server_git_ref=<새 ref>
```

새 PostgreSQL은 `KLOUDVIEW_POSTGRES_PASSWORD`로 `kloudview` role을 만들고, 서버는
처음 시작하면서 빈 스키마를 만듭니다. 5.5와 똑같이 이 스키마를 덤프로 교체합니다.

```bash
cd /opt/kloudview/src
P="docker compose -p kloudview"
$P stop server
$P exec -T postgres dropdb   -U kloudview kloudview
$P exec -T postgres createdb -U kloudview kloudview
$P exec -T postgres pg_restore -U kloudview -d kloudview --no-owner < /opt/kloudview/pg-major.dump
$P start server
```

이 과정 동안 `KLOUDVIEW_POSTGRES_PASSWORD`는 바꾸지 마세요.

### 5.7 포트 변경

서버는 컨테이너 안에서 8080으로 대기하고, `kloudview_server_http_port`는 이를
호스트의 어느 포트로 공개할지 정합니다. `group_vars/all/main.yml`에서 두 값을
함께 바꿉니다.

```yaml
kloudview_server_http_port: "9090"
kloudview_public_url: http://kloudview.example.com:9090
```

```bash
ansible-playbook server.yml
ansible-playbook agents.yml -e kloudview_agent_reinstall=true
```

에이전트가 이미 등록된 상태라면 두 번째 명령이 필요합니다. 에이전트는 설치할 때의
주소를 `/etc/kloudview/agent.env`에 저장해 둡니다. 다시 설치하지 않으면 계속 예전
포트로 접속을 시도하다가 오프라인이 됩니다. 다시 설치하면 이 주소가 새로 쓰이고,
호스트의 identity와 이력은 그대로 유지됩니다.

같은 호스트에 TLS 프록시를 둔다면 `127.0.0.1:8080`으로 지정해 서버를 프록시에만
공개합니다. 이때 `kloudview_public_url`은 프록시의 `https://` 주소가 되고, 내부
포트가 바뀌어도 에이전트를 다시 설치할 필요가 없습니다.

### 5.8 시크릿 변경

| 시크릿 | vault에서 바꿨을 때의 영향 |
|---|---|
| `vault_kloudview_admin_password` | 첫 시작 이후에는 영향 없음. 비밀번호는 콘솔에서 바꿉니다 |
| `vault_kloudview_enrollment_token` | 새로 설치할 때 새 값이 필요합니다. 이미 등록된 에이전트는 자체 credential을 쓰므로 영향이 없습니다 |
| `vault_kloudview_agent_credential_key` | 등록된 모든 에이전트가 인증에 실패하고, `agent.env`에 있는 토큰으로 다시 등록합니다. 토큰도 같이 바꾸려면 모든 에이전트를 다시 설치할 계획이 있을 때만 하세요 |
| `vault_kloudview_postgres_password` | DB는 기존 비밀번호를 유지합니다. PostgreSQL에서 먼저 `ALTER ROLE kloudview PASSWORD '…'`를 실행한 뒤 배포합니다 |

## 6. 문제 해결

| 증상 | 확인할 곳 |
|---|---|
| `Require agent builds` 실패 | `dist/`가 비어 있습니다. `make verify-agent-binaries VERSION=…`을 실행하세요 |
| `Check the target matches the builds` 실패 | `kloudview_server_agent_target_version`이 `dist/VERSION`과 다릅니다 |
| 업그레이드 후 `Start PostgreSQL and the server` 실패 | 서버에서 `docker compose -p kloudview logs server`를 확인하세요. 마이그레이션 실패는 여기에 기록되며, 스키마는 바뀌지 않은 상태입니다 |
| `Fetch the installer`가 503으로 실패 | 서버에 에이전트 빌드가 없습니다. `dist/`를 채운 뒤 `server.yml`을 다시 배포하세요 |
| 에이전트는 설치됐는데 콘솔에 안 보임 | 해당 호스트의 `journalctl -u kloudview-agent`, 그 호스트에서 `kloudview_public_url`에 접속되는지, 호스트명이 중복되지 않는지 확인하세요 |
| `Report unmet hardening items`가 경고를 출력 | 경고마다 [hardening 항목](deployment.md#6-production-hardening-checklist)이 하나씩 적혀 있습니다 |

## 부록: 검증 기록

2026-10-20에 Ubuntu 24.04 머신 한 대를 서버이자 에이전트로 사용해 플레이북을
실행했습니다(`ansible_connection: local`). 실제 배포와 겹치지 않도록 별도의 설치
디렉터리와 Compose 프로젝트를 썼고, 테스트가 끝난 뒤 모두 정리했습니다. 지금
실행 중인 것은 없습니다.

```bash
# 에이전트 빌드 (머신에 make가 없어서 Makefile 레시피를 직접 실행)
docker run --rm --user 1000:1000 -v $PWD:/src -w /src/apps/agent golang:1.24-alpine \
  sh -ec 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags netgo,osusergo \
          -ldflags "-s -w -X main.version=0.0.1-ansibletest" -o /src/dist/kloudview-agent-linux-amd64 ./cmd/kloudview-agent'

cd deploy/ansible
E="-i inventory/hosts.yml -i <test-inventory.yml> --limit kvtest"
ansible-playbook $E server.yml                                   # 배포, /healthz ok
ansible-playbook $E server.yml                                   # 재실행: changed=0
ansible-playbook $E agents.yml -e kloudview_public_url=http://127.0.0.1:8080   # "agent enrolled"
ansible-playbook $E agents.yml -e kloudview_public_url=http://127.0.0.1:8080   # 재실행: changed=0
ansible-playbook $E agents.yml -e ... -e kloudview_agent_state=absent          # 제거, 재실행 changed=0

# 업그레이드와 롤백 경로
ansible-playbook $E server.yml -e kloudview_server_git_ref=HEAD~2 -e kloudview_server_version=old
pg_dump (5.3과 동일)                                             # 스키마 버전 6
ansible-playbook $E server.yml -e kloudview_server_version=new   # 재빌드, 재생성, /healthz "new"
서버 중지, dropdb, createdb, pg_restore (5.5와 동일), 서버 시작   # 복원 완료, 서버 정상
```

검증하지 않은 것: 별도 머신에 SSH로 배포하기, TLS 프록시, 에이전트 자동 업데이트
롤아웃, PostgreSQL 메이저 업그레이드(5.6).
