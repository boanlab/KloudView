// Korean localization for the KloudView web console.
//
// The default language is English. Translation is applied at the single setHTML
// choke point (see ui.js), so every rendered view, modal, and toast is covered
// without touching the individual render functions: the dictionary maps the
// exact visible English text of a DOM text node (or user-facing attribute) to
// its Korean equivalent, and translateFragment rewrites those nodes in place.

const STORAGE_KEY = "kv-lang";

function readStoredLang() {
  try {
    if (typeof localStorage !== "undefined") {
      return localStorage.getItem(STORAGE_KEY) === "ko" ? "ko" : "en";
    }
  } catch {
    // Storage is unavailable (e.g. Node test runner); fall back to English.
  }
  return "en";
}

let lang = readStoredLang();

export function getLang() {
  return lang;
}

export function setLang(value) {
  lang = value === "ko" ? "ko" : "en";
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // Ignore storage failures; the choice simply will not persist.
  }
  if (typeof document !== "undefined") document.documentElement.lang = lang;
}

// EN -> KO dictionary. Each key is the exact visible text of one text node or a
// user-facing attribute value, trimmed of surrounding whitespace. Anything not
// present here (hostnames, numbers, IPs, dynamic data) is left untranslated.
// Note: keys use the literal "&" a parsed HTML text node exposes, not "&amp;".
export const ko = {
  "Share of assigned memory": "할당된 메모리 대비",
  "Share of its own cores": "자신의 코어 대비",
  "View the session recording": "세션 녹화 보기",
  "View the task": "작업 보기",
  "Nothing has changed this host since it was declared": "선언 이후 이 호스트를 바꾼 조치가 없습니다",

  "recorded elsewhere": "다른 곳에 기록됨",
  "No timeline entry yet": "아직 타임라인 항목이 없습니다",
  // Shell, navigation, and identity
  KloudView: "KloudView",
  "KloudView — Infrastructure Operations": "KloudView — 인프라 운영",
  Settings: "설정",
  "Platform Administrator": "플랫폼 관리자",
  Administrator: "관리자",
  Operator: "운영자",
  Viewer: "뷰어",
  Approver: "승인자",
  "Search nodes, VMs, IPs, containers…": "노드, VM, IP, 컨테이너 검색…",
  "Open sidebar": "사이드바 열기",
  "Close sidebar": "사이드바 닫기",
  OPERATIONS: "운영",
  RESOURCES: "리소스",
  MANAGEMENT: "관리",
  GOVERNANCE: "거버넌스",
  Systems: "시스템",
  Issues: "문제",
  Actions: "작업",
  Back: "뒤로",
  "Edit profile": "프로필 수정",
  "Change password (optional)": "비밀번호 변경 (선택)",
  USERNAME: "사용자 이름",
  "DISPLAY NAME": "표시 이름",
  PASSWORD: "비밀번호",
  "CONFIRM PASSWORD": "비밀번호 확인",
  "CURRENT PASSWORD": "현재 비밀번호",
  "NEW PASSWORD": "새 비밀번호",
  "CONFIRM NEW PASSWORD": "새 비밀번호 확인",
  "Save changes": "변경 저장",
  "Console settings": "콘솔 설정",
  Logout: "로그아웃",
  Login: "로그인",
  "Signed in": "로그인됨",
  Resources: "리소스",
  Topology: "토폴로지",
  "By location": "위치별",
  "By node": "노드별",
  "By service": "서비스별",
  "Auto-groups": "자동 그룹",
  "Node inventory": "노드 인벤토리",
  Alerts: "알림",
  "Alert delivery": "알림 전달",
  "Remote shell": "원격 접속",
  Automations: "자동화",
  Agents: "에이전트",
  "Task history": "작업 내역",
  Operations: "운영",
  "Audit log": "감사 로그",
  Fullscreen: "전체 화면",
  Unmanaged: "비관리 리소스",
  "People & access": "사용자·권한",
  "User Management": "사용자 관리",
  "Group Management": "그룹 관리",
  "Resource management": "리소스 관리",
  "Unmanaged resources": "비관리 리소스",
  Alerting: "알림 설정",
  "Permission profiles": "권한 프로필",
  "Access areas": "접근 범위",
  Assignments: "권한 부여",
  History: "변경 기록",
  Users: "사용자",
  Teams: "팀",
  Dashboard: "대시보드",
  Utilization: "사용률",
  "Capacity & usage": "용량·사용량",
  Rightsizing: "라이트사이징",
  "Reclaim candidates": "회수 후보",
  "Scale candidates": "증설 후보",
  "Trends & Forecast": "추세·예측",
  "Capacity forecast": "용량 예측",
  "Overall utilization trend": "전체 이용률 추세",
  "Linear projection from recent trend": "최근 추세 기반 선형 예측",
  "No exhaustion projected": "소진 예측 없음",
  "At capacity now": "현재 용량 한계",
  "Not enough history yet for a trend": "추세 분석에 필요한 이력이 부족합니다",
  "Total CPU": "전체 CPU",
  "Total memory": "전체 메모리",
  "Nodes by usage": "사용량별 노드",
  "Group rollup": "그룹 롤업",
  "Approval-controlled, audited terminal sessions to managed nodes":
    "승인을 거쳐 열리고 전 과정이 기록되는 관리 노드 접속",
  "Reviewable runbooks for infrastructure diagnosis and recovery":
    "검토를 거쳐 실행하는 인프라 진단·복구 절차",
  "Approved operations executed through managed agents":
    "승인 후 에이전트를 통해 실행된 작업",
  "ALL TASKS": "전체 작업",
  "Every request": "요청 전체",
  "Executing now": "실행 중인 작업",
  "AWAITING APPROVAL": "승인 대기",
  "Blocked on review": "검토 대기 중",
  FAILED: "실패",
  "Needs attention": "확인 필요",
  "Installed capacity": "설치 용량",
  Overview: "개요",
  "Active alerts": "활성 알림",
  "Resource facts": "리소스 정보",
  "Share of all cores": "전체 코어 대비",
  "Share of installed memory": "설치 메모리 대비",
  "Share of disk capacity": "디스크 용량 대비",
  "No output recorded": "기록된 출력 없음",
  "No step operations recorded": "기록된 단계 작업이 없습니다",
  Copy: "복사",
  Copied: "복사됨",
  "STEPS EXECUTED": "실행된 단계",
  "No alert matches this filter": "조건에 맞는 알림이 없습니다",
  "No incident matches this filter": "조건에 맞는 장애가 없습니다",
  Evaluate: "확인",
  "Run the check to see the decision.": "확인을 누르면 판정 결과가 표시됩니다.",
  ACTION: "동작",
  "SCOPE PATH": "범위 경로",
  Allowed: "허용",
  Denied: "거부",
  "Evaluation failed": "판정 실패",
  "Open node": "노드 열기",
  "Agent unavailable": "에이전트 없음",
  "This agent is no longer registered.": "이 에이전트는 더 이상 등록되어 있지 않습니다.",
  Live: "실시간",
  "Workloads by usage": "사용량별 워크로드",
  "Filter workloads…": "워크로드 검색",
  "No workload matches the current filters": "조건에 맞는 워크로드가 없습니다",
  "No limit": "제한 없음",
  COLLECTION: "수집 항목",
  "Running on this host": "이 호스트에서 실행 중",
  "No target available": "대상이 없습니다",
  "No agent is connected to open a session on":
    "세션을 열 수 있는 연결된 에이전트가 없습니다",
  "Filter services…": "서비스 검색",
  "No service matches the current filters": "조건에 맞는 서비스가 없습니다",
  Inactive: "비활성",
  "Issue a token, then run the command it produces on the target host.":
    "토큰을 발급한 뒤, 나온 명령을 대상 호스트에서 실행하세요.",
  "Shown once. A bound token is refused from any other host and expires whether or not it is used.":
    "한 번만 표시됩니다. 제한을 건 토큰은 다른 호스트에서 거부되며, 사용 여부와 무관하게 만료됩니다.",
  "Reconnects after a reboot without a new token.":
    "재부팅 후에도 새 토큰 없이 다시 연결합니다.",
  "The installer grants only the group each enabled collection needs.":
    "설치 스크립트는 선택한 수집 항목에 필요한 그룹만 부여합니다.",
  "Verifies the checksum, installs the service, and enrols. Run before the token expires.":
    "체크섬을 검증하고 서비스로 등록한 뒤 연결합니다. 토큰 만료 전에 실행하세요.",
  "ONE-LINE INSTALL": "한 줄 설치",
  "Reads the system journal and /var/log. Needs the systemd-journal and adm groups.":
    "시스템 저널과 /var/log를 읽습니다. systemd-journal·adm 그룹이 필요합니다.",
  "Discovers containers and reads their cgroup usage. Needs the container runtime group.":
    "컨테이너를 탐지하고 cgroup 사용량을 읽습니다. 컨테이너 런타임 그룹이 필요합니다.",
  "Offers approval-gated shell sessions on this host.":
    "이 호스트에서 승인 기반 셸 세션을 제공합니다.",
  "Attach resource": "리소스 연결",
  "Detach resource": "리소스 연결 해제",
  Attach: "연결",
  Detach: "연결 해제",
  "Nothing to attach": "연결할 리소스가 없습니다",
  "Every unmanaged resource below this one is already attached":
    "하위에 해당하는 비관리 리소스가 모두 이미 연결되어 있습니다",
  "Links a manually registered resource to this host so it appears under its sub-resources and inherits scope through the link.":
    "직접 등록한 리소스를 이 호스트에 연결해 하위 리소스로 보이게 하고, 연결을 통해 범위를 상속합니다.",
  "Only resources below this one in the hierarchy are offered: node > vm > container > process.":
    "계층상 이 리소스보다 아래인 것만 제시됩니다: 노드 > VM > 컨테이너 > 프로세스.",
  "Agent-managed resources are attached by their own inventory and are not listed here.":
    "에이전트가 관리하는 리소스는 자체 인벤토리로 연결되므로 여기에 나오지 않습니다.",
  "hosts and runs are ownership; contains is grouping; depends_on records a dependency without ownership, so it does not propagate scope.":
    "hosts·runs는 소유, contains는 묶음, depends_on은 소유 없는 의존 관계라 범위를 전파하지 않습니다.",
  "This resource type does not report usage yet":
    "이 리소스 유형은 아직 사용량을 보고하지 않습니다",
  "Memory used": "사용 메모리",
  Limit: "제한",
  Workload: "워크로드",
  "Live stream": "실시간 스트림",
  "Logins and kernel activity stream in as they happen; everything else waits on the node for a read":
    "로그인과 커널 활동은 발생 즉시 올라오고, 나머지는 노드에 남아 조회로 확인합니다",
  "Live": "실시간",
  "By CPU": "CPU 순",
  "By memory": "메모리 순",
  "not sampled": "건은 수집 대상 아님",
  "Containers and VMs are always measured.": "컨테이너와 VM은 항상 측정됩니다.",
  "Closed": "종료",
  "commands sent": "개 명령 전송",
  "A full-screen program has the terminal — click the screen to use it": "전체 화면 프로그램이 터미널을 쓰는 중입니다 — 화면을 클릭해 조작하세요",
  "lines selected": "줄 선택됨",
  "Attach to incident": "장애에 첨부",
  "Clear": "선택 해제",
  "Declare an incident to attach them to": "첨부하려면 장애를 먼저 선언하세요",
  "WHY THIS MATTERS": "왜 중요한가",
  "What these lines show": "이 줄들이 보여주는 것",
  "Attach": "첨부",
  "Attached": "첨부됨",
  "The node answered, but the output is no longer held. Read again.": "노드는 답했지만 결과가 더 이상 보관되지 않습니다. 다시 조회하세요.",
  "from containers": "컨테이너에서",
  "Logins and kernel activity stream here. In the same window the node logged": "로그인과 커널 활동만 여기로 옵니다. 같은 창에 이 노드가 기록한 것은",
  "from this host": "이 호스트에서",
  "from its containers": "이 노드의 컨테이너에서",
  "read either from the node.": "— 어느 쪽이든 노드에서 조회하세요.",
  "Read from node": "노드에서 조회",
  "No access or kernel activity in this window.": "이 창에 접속·커널 활동이 없습니다.",
  "This host": "이 호스트",
  "Logins and sudo": "로그인·sudo",
  "Everything": "전부",
  "Any severity": "모든 심각도",
  "Errors": "오류",
  "Warnings and worse": "경고 이상",
  "Below warning": "경고 미만",
  "Notice": "알림(notice)",
  "Info": "정보(info)",
  "Debug": "디버그",
  "System, login, and kernel activity streams in as it happens; a journal read reaches further back":
    "시스템·로그인·커널 활동이 발생 즉시 들어오며, 더 이전 구간은 저널 조회로 확인합니다",
  "Nothing has been logged in this window.": "이 구간에 기록된 로그가 없습니다.",
  "No system activity in this window.": "이 구간에 시스템 활동이 없습니다.",
  "No login or sudo activity in this window.":
    "이 구간에 로그인·sudo 활동이 없습니다.",
  "No kernel activity in this window.": "이 구간에 커널 활동이 없습니다.",
  "Streamed as they happen": "발생 즉시 수집",
  "Error and worse": "오류 이상",
  "All nodes": "전체 노드",
  "Last hour": "최근 1시간",
  Unit: "유닛",
  Terminated: "종료됨",
  "Read-only": "읽기 전용",
  "No samples retained for this window": "이 구간에 보관된 표본이 없습니다",
  "Not enough samples yet": "표본이 아직 부족합니다",
  "This process is not among the ones sampled for a trend": "이 프로세스는 추세 수집 대상이 아닙니다",
  "Only the heaviest by CPU and by memory are measured each tick.": "매 주기마다 CPU와 메모리가 가장 무거운 것만 측정합니다.",
  "Sampled, but not long enough yet for a line": "수집 중이지만 선을 그리기엔 아직 짧습니다",
  "See the container's trend": "컨테이너 추세 보기",
  "See the host's trend": "호스트 추세 보기",
  "Running now": "현재 실행 중",
  "THREADS": "스레드",
  "Show VMs, containers, and processes that stopped being reported":
    "보고가 끊긴 VM·컨테이너·프로세스 보기",
  "Linked alerts": "연결된 알림",
  "No alert was linked to this incident": "이 장애에 연결된 알림이 없습니다",
  "Affected resources": "영향받는 리소스",
  "Edit incident": "장애 수정",
  "Save incident": "장애 저장",
  "AFFECTED RESOURCES": "영향받는 리소스",
  "RELATED ALERTS": "관련 알림",
  "Hold Ctrl or Cmd to select more than one.": "여러 개를 선택하려면 Ctrl 또는 Cmd를 누른 채 클릭하세요.",
  "No open alerts": "열린 알림 없음",
  "Filter users…": "사용자 검색",
  "HIERARCHY PATHS": "계층 경로",
  "DYNAMIC SELECTOR": "동적 선택 조건",
  "Resources carrying every tag join this group automatically.":
    "모든 태그를 가진 리소스가 이 그룹에 자동으로 포함됩니다.",
  "Only resources carrying every tag are evaluated. Leave empty to match the whole scope.":
    "모든 태그를 가진 리소스만 평가합니다. 비워 두면 범위 전체가 대상입니다.",
  Add: "추가",
  "Custom…": "직접 입력…",
  "A local account or a team.": "로컬 계정 또는 팀.",
  "Select none to compare alerts on the same resource only.":
    "선택하지 않으면 같은 리소스의 알림만 비교합니다.",
  "Only used by the service operations.": "서비스 작업에서만 사용합니다.",
  OFFLINE: "연결 끊김",
  "Waiting for the first update": "첫 갱신 대기 중",
  "Filter resources…": "리소스 검색",
  "Filter alerts…": "알림 검색",
  "Incident declared": "장애 선언됨",
  "Click a row to inspect": "행을 클릭하면 상세를 볼 수 있습니다",
  "No timeline entry yet": "아직 타임라인 기록이 없습니다",
  "No resource linked to this incident": "이 장애에 연결된 리소스가 없습니다",
  "Network (RX rate)": "네트워크 (수신 처리량)",
  "Network (TX rate)": "네트워크 (송신 처리량)",
  "Share of all cores, averaged over the sample.":
    "표본 구간 평균, 전체 코어 대비 사용률.",
  "Share of installed memory in use.": "설치된 메모리 대비 사용률.",
  "Share of the root filesystem in use.": "루트 파일시스템 대비 사용률.",
  "Inbound throughput on the node's interfaces.":
    "노드 인터페이스로 들어오는 초당 처리량.",
  "Outbound throughput on the node's interfaces.":
    "노드 인터페이스에서 나가는 초당 처리량.",
  "Timeline entry": "타임라인 항목",
  "View entry": "항목 보기",
  Recorded: "기록 시각",
  Entry: "항목 ID",
  MESSAGE: "메시지",
  DETAILS: "세부 정보",
  Nodes: "노드",
  "Trends & forecast": "추세·예측",
  "Receive / transmit": "수신 / 송신",
  Size: "크기",
  Model: "모델",
  State: "상태",
  Device: "장치",
  Interface: "인터페이스",
  Addresses: "주소",
  MTU: "MTU",
  Runtime: "런타임",
  PID: "PID",
  RSS: "RSS",
  Sub: "세부",
  "ENROLLMENT TOKEN": "등록 토큰",
  "Valid 1 minute": "1분간 유효",
  "Valid 5 minutes": "5분간 유효",
  "Valid 15 minutes": "15분간 유효",
  "Valid 1 hour": "1시간 유효",
  "Bind to hostname (optional)": "호스트명 제한 (선택)",
  "Bind to CIDR (optional)": "대역 제한 (선택)",
  "Issue token": "토큰 발급",
  "Required to change your own password.": "본인 비밀번호를 변경하려면 필요합니다.",
  "Enter your current password to change it": "현재 비밀번호를 입력하세요",
  "New password and confirmation do not match": "새 비밀번호와 확인이 일치하지 않습니다",
  "current password is incorrect": "현재 비밀번호가 올바르지 않습니다",
  ACCOUNTS: "계정",
  "Local accounts": "로컬 계정",
  DISABLED: "비활성",
  "Can sign in": "로그인 가능",
  "Sign-in blocked": "로그인 차단됨",
  TEAMS: "팀",
  "Groupings for bindings": "권한 부여용 그룹",
  "What is this?": "이건 무엇인가요?",
  "Unmanaged resource": "비관리 리소스",
  "Node group": "노드 그룹",
  "Alert rule": "알림 규칙",
  "Silence window": "무음 창",
  Incident: "장애",
  Account: "계정",
  Role: "역할",
  "Role binding": "역할 바인딩",
  Runbook: "런북",
  "Agent task": "에이전트 작업",
  "Terminal session": "터미널 세션",
  "Notification channel": "알림 채널",
  "Notification route": "알림 경로",
  "Inhibition policy": "중복 알림 차단 규칙",
  "For hosts and services no agent reports on. An agent-managed resource is created automatically and cannot be deleted here.":
    "에이전트가 보고하지 않는 호스트·서비스를 위한 항목입니다. 에이전트가 관리하는 리소스는 자동 생성되며 여기서 삭제할 수 없습니다.",
  "Type places it in the hierarchy: node > vm > container > process.":
    "유형이 계층 위치를 정합니다: 노드 > VM > 컨테이너 > 프로세스.",
  "Health is what the console shows until a metric or alert says otherwise.":
    "상태는 메트릭이나 알림이 갱신하기 전까지 콘솔에 표시되는 값입니다.",
  "Tags drive dynamic group membership and alert rule selectors. Use key=value pairs separated by commas.":
    "태그는 동적 그룹 편입과 알림 규칙 조건에 사용됩니다. key=value를 쉼표로 구분해 입력하세요.",
  "Groups aggregate health and capacity, and a group's path is the scope a role binding can be limited to.":
    "그룹은 상태와 용량을 집계하며, 그룹 경로는 역할 바인딩을 제한하는 범위가 됩니다.",
  "Type is what the group means: rack, cluster, service, or zone. The infrastructure map groups by it.":
    "유형은 그룹의 의미입니다: 랙·클러스터·서비스·존. 인프라 맵이 이 값으로 묶습니다.",
  "Path is the hierarchy position, such as production/dc-1/rack-08. Scopes match on it by prefix.":
    "경로는 계층 위치입니다(예: production/dc-1/rack-08). 범위는 접두사로 일치시킵니다.",
  "Static membership is chosen by hand; dynamic membership follows the tag selector and updates itself.":
    "정적 멤버십은 직접 선택하고, 동적 멤버십은 태그 조건을 따라 자동으로 갱신됩니다.",
  "An alert is raised only after the condition holds continuously for the duration.":
    "조건이 지속 시간 동안 계속 유지될 때만 알림이 발생합니다.",
  "CPU, memory, and disk are percentages of capacity. The network metrics are a byte rate; pick the unit beside the threshold.":
    "CPU·메모리·디스크는 용량 대비 백분율입니다. 네트워크는 초당 바이트이며 임계값 옆에서 단위를 고르세요.",
  "Scope limits the rule to a hierarchy path; the tag selector narrows it further to resources carrying every tag.":
    "범위는 규칙을 계층 경로로 한정하고, 태그 조건은 모든 태그를 가진 리소스로 더 좁힙니다.",
  "Editing, disabling, or deleting a rule resolves the alerts it raised and restarts its duration.":
    "규칙을 수정·비활성·삭제하면 그 규칙이 만든 알림이 해결되고 지속 시간이 초기화됩니다.",
  "Install by hand instead": "직접 설치하기",
  "1 · ENROLLMENT TOKEN": "1 · 등록 토큰",
  "ONE-SHOT INSTALL": "한 번에 설치",
  "This server is reachable over plain HTTP, so the installer is downloaded and read before it runs rather than piped into a shell. Serve the console over HTTPS to use the one-line form.":
    "이 서버는 평문 HTTP로 접근되므로, 설치 스크립트를 셸로 바로 넘기지 않고 내려받아 확인한 뒤 실행합니다. HTTPS로 서비스하면 한 줄 형태를 사용할 수 있습니다.",
  Done: "완료",
  SESSIONS: "세션",
  CLOSED: "종료됨",
  RUNBOOKS: "런북",
  "Requested in total": "총 요청",
  "Open right now": "현재 열림",
  "Ended, recording kept": "종료됨, 녹화 보관",
  "Defined procedures": "정의된 절차",
  "Completed runs": "완료된 실행",
  "Filter by target, requester, or reason…": "대상·요청자·사유 검색",
  "No sessions requested": "요청된 세션이 없습니다",
  "+ Add user": "+ 사용자 추가",
  "+ Add team": "+ 팀 추가",
  "+ Auto-group by label": "+ 라벨로 자동 그룹",
  Accounts: "계정",
  Username: "사용자 이름",
  "Display name": "표시 이름",
  "Manage local accounts and access status": "로컬 계정과 접근 상태를 관리합니다",
  "Group users for ownership and organization": "담당과 조직 구분을 위해 사용자를 묶습니다",
  "Connection health, versions, capabilities, and reported hardware":
    "연결 상태, 버전, 기능, 보고된 하드웨어",
  "Capacity-aware usage, headroom, and idle or saturated nodes":
    "용량 기준 사용률과 여유, 유휴·포화 노드",
  "Manually catalogued resources without an installed agent":
    "에이전트 없이 직접 등록한 리소스",
  "Real-time health, capacity, and alerts across your infrastructure":
    "인프라 전체의 실시간 상태·용량·알림",
  "All resources healthy · no active alerts": "모든 리소스 정상 · 활성 알림 없음",
  "Live telemetry": "실시간 수집",
  "TOTAL CPU": "전체 CPU",
  "TOTAL MEMORY": "전체 메모리",
  "CPU HEADROOM": "CPU 여유",
  IDLE: "유휴",
  SATURATED: "포화",
  "cores available": "코어 사용 가능",
  "under 10% used": "사용률 10% 미만",
  "over 85% used": "사용률 85% 초과",
  "Cores used": "사용 코어",
  Cores: "코어",
  "Avg CPU": "평균 CPU",
  "Avg Mem": "평균 메모리",
  Reclaimable: "회수 가능",
  "No sustained-saturated nodes": "지속 포화 노드 없음",
  Now: "현재",
  Trend: "추세",
  "In 30 days": "30일 후",
  Relation: "관계",
  All: "전체",
  "No active session — request one and get it approved to open a shell.":
    "활성 세션이 없습니다 — 세션을 요청하고 승인받으면 셸이 열립니다.",
  "No resources match the current filters": "조건에 맞는 리소스가 없습니다",
  "Record of every change across the API, agents, approvals, and terminals":
    "API·에이전트·승인·터미널의 모든 변경 기록",
  "Filter by name, type, or id…": "이름·유형·ID로 검색…",
  Rack: "랙",
  Label: "라벨",
  Service: "서비스",
  Zone: "존",
  CONTAINER: "컨테이너",
  PROCESS: "프로세스",
  "> 2 years": "2년 이상",
  "< 1 day": "1일 미만",
  "NETWORK RX": "네트워크 수신",
  "NETWORK TX": "네트워크 송신",
  "STATE AT THIS MOMENT": "이 시점의 상태",
  "No metric sample covers this moment": "이 시점을 포함하는 메트릭 표본이 없습니다",
  "Loading…": "불러오는 중…",
  Offset: "시간차",
  Memory: "메모리",
  "Add user": "사용자 추가",
  "Edit user": "사용자 수정",
  "Add team": "팀 추가",
  "Create group": "그룹 생성",
  "Create groups": "그룹 생성",
  "Auto-group by label": "라벨로 자동 그룹",
  "Create a dynamic group for each distinct value of a label. Matching resources — including nodes added later — are grouped automatically.":
    "라벨 값마다 동적 그룹을 만듭니다. 나중에 추가되는 노드를 포함해 해당하는 리소스가 자동으로 묶입니다.",
  LABEL: "라벨",
  MEMBERS: "구성원",
  "No role (assign later)": "역할 없음 (나중에 지정)",
  "Matching alerts are suppressed for the window; the underlying condition is still evaluated.":
    "이 기간 동안 해당 알림은 전송되지 않습니다. 조건 평가 자체는 계속됩니다.",
  "Existing matching alerts move to silenced when the window opens.":
    "기간이 시작되면 해당하는 기존 알림이 무음 상태로 바뀝니다.",
  "Scope and tag selector decide what matches. Leave the selector empty to cover the whole scope.":
    "범위와 태그 조건이 대상을 정합니다. 조건을 비우면 범위 전체가 대상입니다.",
  "Declare one when something needs coordinated response; the timeline records every change with its author.":
    "협업 대응이 필요할 때 선언합니다. 타임라인에 모든 변경이 담당자와 함께 기록됩니다.",
  "An incident needs at least one resource or alert. Resources of linked alerts are added automatically.":
    "장애에는 리소스나 알림이 최소 하나 필요합니다. 연결한 알림의 리소스는 자동으로 포함됩니다.",
  "The commander is the person accountable for the response, not necessarily the one who declared it.":
    "지휘자는 대응을 책임지는 사람이며, 선언한 사람과 같을 필요는 없습니다.",
  "A runbook is an ordered list of agent operations run against one target node.":
    "런북은 대상 노드 하나에 순서대로 실행되는 에이전트 작업 목록입니다.",
  "Risk decides whether an execution needs a second person's approval before it runs.":
    "위험도가 실행 전 다른 사람의 승인이 필요한지를 결정합니다.",
  "Only operations on the agent's allowlist execute, whatever the runbook asks for.":
    "런북에 무엇이 적혀 있든 에이전트 허용 목록에 있는 작업만 실행됩니다.",
  "Tasks run through the agent on the target node, never over SSH from the server.":
    "작업은 대상 노드의 에이전트를 통해 실행되며, 서버에서 SSH로 접속하지 않습니다.",
  "Service operations are limited to the agent's allowlist, and a restart needs approval.":
    "서비스 작업은 에이전트 허용 목록으로 제한되며, 재시작은 승인이 필요합니다.",
  "The reason is recorded in the audit trail; write what a reviewer would need to know.":
    "사유는 감사 기록에 남습니다. 검토자가 알아야 할 내용을 적으세요.",
  "A session opens only after another person approves it, and every keystroke and byte of output is recorded.":
    "세션은 다른 사람이 승인해야 열리며, 입력과 출력이 모두 기록됩니다.",
  "Recordings are masked for common secret patterns, capped, and expire on their own.":
    "녹화는 일반적인 비밀 패턴을 마스킹하고, 용량 제한과 자동 만료가 적용됩니다.",
  "The session runs as the agent's unprivileged account unless the host configures otherwise.":
    "호스트에서 따로 설정하지 않으면 세션은 에이전트의 비특권 계정으로 실행됩니다.",
  "An account signs in; what it may do comes from role bindings, not from the account itself.":
    "계정은 로그인 수단일 뿐이고, 권한은 계정이 아니라 역할 바인딩에서 옵니다.",
  "Assigning a role here creates the binding for you. Fine-grained access is managed under Role bindings.":
    "여기서 역할을 지정하면 바인딩이 함께 생성됩니다. 세부 권한은 역할 바인딩에서 관리합니다.",
  "Disabling blocks sign-in and ends existing sessions but keeps the account and its audit trail.":
    "비활성화하면 로그인이 막히고 기존 세션이 종료되지만, 계정과 감사 기록은 유지됩니다.",
  "A role is a list of resource and action pairs; * means every resource or every action.":
    "역할은 리소스와 동작의 쌍 목록입니다. *는 모든 리소스 또는 모든 동작을 뜻합니다.",
  "A role grants nothing on its own until a binding attaches it to a subject and a scope.":
    "역할만으로는 아무 권한도 없으며, 바인딩이 주체와 범위에 연결해야 효력이 생깁니다.",
  "System roles cannot be edited, so a working administrator role always remains.":
    "시스템 역할은 수정할 수 없어, 동작하는 관리자 역할이 항상 남습니다.",
  "A scope is the set of hierarchy paths a role binding applies to.":
    "범위는 역할 바인딩이 적용되는 계층 경로의 집합입니다.",
  "Paths match by prefix: production/dc-1 covers everything beneath it.":
    "경로는 접두사로 일치합니다. production/dc-1은 그 하위 전체를 포함합니다.",
  "A tag selector restricts the scope further to resources carrying every tag.":
    "태그 조건은 모든 태그를 가진 리소스로 범위를 더 좁힙니다.",
  "A binding is what actually grants access: it attaches a role to a subject within a scope.":
    "실제로 권한을 부여하는 것은 바인딩입니다. 범위 안에서 주체에 역할을 연결합니다.",
  "The subject is a local account or a team.": "주체는 로컬 계정 또는 팀입니다.",
  "An expiry makes the grant temporary; the binding stops applying once it passes.":
    "만료를 지정하면 한시적 권한이 되며, 지나면 바인딩이 적용되지 않습니다.",
  "A channel is where notifications go; the current type is a generic HTTP webhook.":
    "채널은 알림이 전송되는 곳이며, 현재 지원 형식은 일반 HTTP 웹훅입니다.",
  "Routes decide which alerts reach which channel; a channel referenced by a route cannot be deleted.":
    "어떤 알림이 어느 채널로 갈지는 경로가 정합니다. 경로가 참조하는 채널은 삭제할 수 없습니다.",
  "A route matches alerts by severity, event, scope, and tags, then sends them to its channels.":
    "경로는 심각도·이벤트·범위·태그로 알림을 매칭해 지정된 채널로 보냅니다.",
  "Evaluation stops at the first matching route unless it is set to continue.":
    "계속 진행으로 설정하지 않으면 처음 일치하는 경로에서 평가가 멈춥니다.",
  "An inhibition suppresses notification for target alerts while a matching source alert is active.":
    "원인 알림이 발생 중인 동안 대상 알림의 전송을 멈춥니다.",
  "The alert itself keeps firing; only its delivery is held back.":
    "알림 자체는 계속 발생하며, 전송만 보류됩니다.",
  "Equal labels decide what counts as related. With none, only alerts on the same resource are compared.":
    "동일 라벨이 연관 여부를 정합니다. 지정하지 않으면 같은 리소스의 알림만 비교합니다.",
  "Server URL": "서버 주소",
  "Install the binary": "바이너리 설치",
  "where the agent can replace it during a self-update":
    "자동 업데이트 시 에이전트가 교체할 수 있는 위치",
  Configure: "설정",
  "Install the unit and start it": "유닛 설치 후 시작",
  "environment=production, owner=platform": "environment=production, owner=platform",
  "ip=10.0.0.10, os=linux": "ip=10.0.0.10, os=linux",
  "leave blank to keep current": "비워 두면 현재 값 유지",
  "e.g. DC-1 Production": "예: DC-1 Production",
  "Filter nodes…": "노드 검색",
  "Filter sub-resources…": "세부 리소스 검색",
  "Create user": "사용자 생성",
  "Create team": "팀 생성",
  "Jane Smith": "홍길동",
  "at least 6 characters": "6자 이상",
  "Build the binary with": "다음으로 바이너리를 빌드하세요:",
  Logs: "로그",
  "Read a window of a node's logs on demand; nothing is shipped until you ask":
    "필요할 때 노드 로그의 특정 구간만 읽어옵니다. 요청 전에는 아무것도 전송되지 않습니다",
  "Journal read": "저널 조회",
  System: "시스템",
  Authentication: "인증",
  Secure: "보안",
  Kernel: "커널",
  "Read logs": "로그 읽기",
  "Last 30 minutes": "최근 30분",
  "Last 2 hours": "최근 2시간",
  "Last 24 hours": "최근 24시간",
  "Filter lines…": "줄 검색",
  LINES: "줄",
  ERRORS: "오류",
  WARNINGS: "경고",
  ACCESS: "접근",
  "Read from the node": "노드에서 읽음",
  "Failures and denials": "실패·거부",
  Warnings: "경고",
  "Sessions and sudo": "세션·sudo",
  "Waiting for the agent to answer…": "에이전트 응답 대기 중…",
  "Choose a node and a window, then read the logs": "노드와 구간을 고르고 로그를 읽으세요",
  "No agent-managed node to read from": "읽어올 에이전트 관리 노드가 없습니다",
  "The agent did not answer in time": "에이전트가 제한 시간 내에 응답하지 않았습니다",
  vCPU: "vCPU",
  "Disk I/O": "디스크 I/O",
  "Network I/O": "네트워크 I/O",
  Task: "작업",
  Hardware: "하드웨어",
  "No active alert on this resource": "이 리소스에 활성 알림이 없습니다",
  "No task or session recorded for this resource":
    "이 리소스에 기록된 작업이나 세션이 없습니다",
  "Saturated at 85%": "포화 기준 85%",
  "Receive and transmit": "수신 + 송신",
  "No active alert": "활성 알림 없음",
  "Last update": "최종 갱신",
  "Approved by": "승인자",
  Requested: "요청 시각",
  Ended: "종료 시각",
  "Ran at": "실행 시각",
  Succeeded: "성공",
  Failed: "실패",
  "Awaiting approval": "승인 대기",
  Approved: "승인됨",
  Rejected: "거부됨",
  Closed: "종료",
  "Low risk": "낮음",
  "Medium risk": "보통",
  "High risk": "높음",
  "Deleted runbook": "삭제된 런북",
  "Filter by task, target, requester, or reason…":
    "작업·대상·요청자·사유 검색",
  "Filter executions…": "실행 기록 검색",
  "No task matches this filter": "조건에 맞는 작업이 없습니다",
  "No execution matches this filter": "조건에 맞는 실행 기록이 없습니다",
  "Infrastructure Map": "인프라 맵",
  "Whole-infrastructure view grouped by cluster, rack, or label — colored by load":
    "클러스터·랙·라벨로 묶어 부하에 따라 색으로 표시한 전체 인프라 화면",
  "Filter nodes and groups…": "노드·그룹 검색",
  "No node matches this filter": "조건에 맞는 노드가 없습니다",
  "Sub-resources": "세부 리소스",
  "No sub-resources": "세부 리소스 없음",
  Ungrouped: "그룹 없음",
  Idle: "유휴",
  Normal: "정상",
  Busy: "바쁨",
  Saturated: "포화",
  "By cluster": "클러스터별",
  "By rack": "랙별",
  "By label": "라벨별",
  "By zone": "존별",
  "View utilization": "이용률 보기",
  saturated: "포화",
  idle: "유휴",
  "Alert rules": "알림 규칙",
  "Notification routing": "알림 라우팅",
  Incidents: "장애",
  Infrastructure: "인프라",
  "All resources": "전체 리소스",
  "Physical hierarchy": "물리 계층",
  "Compute hierarchy": "컴퓨트 계층",
  "Service hierarchy": "서비스 계층",
  Groups: "그룹",
  "Node groups": "노드 그룹",
  "Dynamic groups": "동적 그룹",
  Tags: "태그",
  Inventory: "인벤토리",
  "Agent Fleet": "에이전트 플릿",
  Terminal: "터미널",
  Runbooks: "런북",
  Jobs: "작업",
  "Access Control": "접근 제어",
  Roles: "역할",
  Scopes: "범위",
  "Role bindings": "역할 바인딩",
  "Changes & Audit": "변경 & 감사",
  "Changes & audit": "변경 & 감사",

  // Status and severity words
  Critical: "심각",
  Emergency: "긴급",
  Notice: "공지",
  Debug: "디버그",
  Warning: "경고",
  Error: "오류",
  Info: "정보",
  Address: "주소",
  Healthy: "정상",
  Degraded: "저하",
  Offline: "오프라인",
  Online: "온라인",
  Unknown: "알 수 없음",
  Firing: "발생",
  Acknowledged: "확인됨",
  Silenced: "무음",
  Resolved: "해결됨",
  Active: "활성",
  Expired: "만료됨",
  Scheduled: "예정됨",
  healthy: "정상",
  warning: "경고",
  critical: "심각",
  unknown: "알 수 없음",
  connected: "연결됨",
  offline: "오프라인",
  static: "정적",
  dynamic: "동적",
  operator: "운영자",
  waiting: "대기 중",
  // Status values render lowercase in tables and pills; the capitalized keys
  // above only match prose, so each needs its own entry.
  resolved: "해결됨",
  firing: "발생",
  acknowledged: "확인됨",
  silenced: "무음",
  online: "온라인",
  active: "활성",
  expired: "만료됨",
  degraded: "저하",
  maintenance: "점검 중",
  // Log severities, as journald reports them.
  emerg: "긴급",
  alert: "위험",
  crit: "심각",
  err: "오류",
  notice: "알림",
  info: "정보",
  debug: "디버그",
  // Node group types.
  cluster: "클러스터",
  rack: "랙",
  service: "서비스",
  label: "레이블",
  // Agent capabilities, granted at install time.
  inventory: "인벤토리",
  metrics: "메트릭",
  terminal: "터미널",
  logs: "로그",


  // Overview
  "Infrastructure overview": "인프라 개요",
  "Production · Live Server aggregation": "프로덕션 · 실시간 서버 집계",
  "NOC mode": "NOC 모드",
  Customize: "사용자 지정",
  "+ Create group": "+ 그룹 생성",
  "TOTAL RESOURCES": "전체 리소스",
  CRITICAL: "심각",
  WARNING: "경고",
  "AGENT COVERAGE": "에이전트 커버리지",
  "ACTIVE INCIDENTS": "활성 장애",
  "Aggregated resource health": "집계된 리소스 상태",
  "Online reporting agents": "온라인 보고 에이전트",
  "Open incident workflow": "진행 중인 장애 워크플로",
  "Group health": "그룹 상태",
  "Attention required": "주의 필요",
  "View all": "전체 보기",
  "Infrastructure heatmap": "인프라 히트맵",
  "Resource utilization": "리소스 사용률",
  "Network throughput": "네트워크 처리량",
  "Last 1 hour": "최근 1시간",
  "Last 6 hours": "최근 6시간",
  "No active alerts": "활성 알림 없음",
  "Absolute throughput": "절대 처리량",
  "Stale / idle": "오래됨 / 유휴",
  "Below 75%": "75% 미만",
  "75–90%": "75–90%",
  "90%+": "90% 이상",
  Stale: "오래됨",
  "No resources match the current fleet filters":
    "현재 플릿 필터와 일치하는 리소스가 없습니다",
  "Waiting for metric history": "메트릭 기록 대기 중",
  Health: "상태",
  "All groups": "전체 그룹",
  "All states": "전체 상태",
  "Anomalies first": "이상 항목 우선",

  // Metric tiles
  CPU: "CPU",
  Disk: "디스크",
  Network: "네트워크",
  "Live Agent data": "실시간 에이전트 데이터",
  "LIVE CPU": "실시간 CPU",
  "LIVE MEMORY": "실시간 메모리",
  "LIVE DISK": "실시간 디스크",
  "Current receive rate": "현재 수신 처리량",
  "Current transmit rate": "현재 송신 처리량",

  // Tables and common columns
  Resource: "리소스",
  resources: "리소스",
  Status: "상태",
  Group: "그룹",
  Workloads: "워크로드",
  Agent: "에이전트",
  "IP Address": "IP 주소",
  "IP address": "IP 주소",
  Updated: "업데이트됨",
  Action: "동작",
  Edit: "편집",
  Delete: "삭제",
  Remove: "제거",
  Save: "저장",
  Cancel: "취소",
  Confirm: "확인",
  Apply: "적용",
  Create: "생성",
  Type: "유형",
  Target: "대상",
  Source: "소스",
  Created: "생성됨",
  Name: "이름",
  Mode: "모드",
  Path: "경로",
  Members: "멤버",
  "Agent managed": "에이전트 관리됨",

  // Infrastructure explorer / detail
  "Infrastructure explorer": "인프라 탐색기",
  "Browse and manage every physical, virtual, and container resource":
    "모든 물리·가상·컨테이너 리소스를 탐색하고 관리합니다",
  Export: "내보내기",
  "+ Add resource": "+ 리소스 추가",
  "Bare metal": "베어메탈",
  Hypervisor: "하이퍼바이저",
  Node: "노드",
  VM: "VM",
  Container: "컨테이너",
  Process: "프로세스",
  "Critical only": "심각만",
  Hypervisors: "하이퍼바이저",
  VMs: "VM",
  Containers: "컨테이너",
  Processes: "프로세스",
  "Live server-side aggregation": "실시간 서버 집계",
  "Server unavailable": "서버 사용 불가",
  "All systems operational": "모든 시스템 정상",
  "Action required": "조치 필요",
  "Warnings to review": "검토할 경고",
  "Agents online": "온라인 에이전트",
  Previous: "이전",
  Next: "다음",
  List: "목록",
  Heatmap: "히트맵",
  "Open terminal": "터미널 열기",
  MEMORY: "메모리",
  DISK: "디스크",
  NETWORK: "네트워크",
  HEALTH: "상태",
  "Latest Agent sample": "최신 에이전트 샘플",
  "RX and TX rate": "수신 + 송신 처리량",
  "Alert-adjusted state": "알림 반영 상태",
  Attributes: "속성",
  "No attributes": "속성 없음",
  Relations: "관계",
  "No relations": "관계 없음",
  "Monitor, acknowledge, and resolve infrastructure anomalies":
    "인프라 이상을 모니터링, 확인, 해결합니다",
  "Silence rules": "무음 규칙",
  "+ New alert rule": "+ 새 알림 규칙",
  FIRING: "발생",
  ACKNOWLEDGED: "확인됨",
  RESOLVED: "해결됨",
  "Active conditions": "활성 조건",
  "Under investigation": "조사 중",
  "Immediate attention": "즉시 조치 필요",
  "Historical alerts": "과거 알림",
  "Metric rule condition": "메트릭 규칙 조건",
  Acknowledge: "확인",
  Resolve: "해결",
  "No server alerts": "서버 알림 없음",
  "DISK I/O": "디스크 I/O",
  Uptime: "가동 시간",
  Restarts: "재시작 횟수",
  Running: "실행 중",
  Restart: "재시작",
  production: "프로덕션",
  payment: "결제",
  "Last seen 8 seconds ago": "8초 전 마지막 확인",
  Maintenance: "유지보수",
  Summary: "요약",
  Metrics: "메트릭",
  Services: "서비스",
  Events: "이벤트",
  "System information": "시스템 정보",
  Hostname: "호스트명",
  "Operating system": "운영 체제",
  "Agent version": "에이전트 버전",
  Serial: "시리얼",
  "Resource relations": "리소스 관계",
  "Physical parent": "물리 상위",

  // Hierarchy pages
  "Group path → Rack → Node": "그룹 경로 → 랙 → 노드",
  "Host → Hypervisor → Virtual machine → Runtime":
    "호스트 → 하이퍼바이저 → 가상 머신 → 런타임",
  "Service group → Container → Process": "서비스 그룹 → 컨테이너 → 프로세스",
  "+ Add group": "+ 그룹 추가",
  "+ Add relation": "+ 관계 추가",
  "Live hierarchy": "실시간 계층",
  "No matching hierarchy data": "일치하는 계층 데이터 없음",
  "Region → Datacenter → Room → Rack → Node":
    "리전 → 데이터센터 → 룸 → 랙 → 노드",
  "Cluster → Hypervisor → Virtual machine": "클러스터 → 하이퍼바이저 → 가상 머신",
  "Service → Component → Workload → Runtime":
    "서비스 → 컴포넌트 → 워크로드 → 런타임",
  "+ Add item": "+ 항목 추가",
  Hierarchy: "계층",
  "Expand all": "모두 펼치기",
  "Selected scope": "선택된 범위",
  Owner: "소유자",
  "Access scope": "접근 범위",
  "● Degraded": "● 저하",
  "View resources": "리소스 보기",

  // Groups / tags
  "Selector-managed membership based on resource tags":
    "리소스 태그 기반 셀렉터 관리 멤버십",
  "Manage physical and logical resource groups":
    "물리 및 논리 리소스 그룹을 관리합니다",
  "No groups configured": "구성된 그룹 없음",
  "Resource tags": "리소스 태그",
  "Tag index used by dynamic groups, alert selectors, and access scopes":
    "동적 그룹, 알림 셀렉터, 접근 범위에서 사용하는 태그 인덱스",
  "+ Add tagged resource": "+ 태그된 리소스 추가",
  Key: "키",
  Value: "값",
  Types: "유형",
  "No resource tags": "리소스 태그 없음",

  // Fleet / inventory
  "Agent fleet": "에이전트 플릿",
  "Registration, health, capabilities, and rollout management":
    "등록, 상태, 기능, 롤아웃 관리",
  "Install agent": "에이전트 설치",
  "Upgrade agents": "에이전트 업그레이드",
  REGISTERED: "등록됨",
  ONLINE: "온라인",
  INVENTORY: "인벤토리",
  "LATEST VERSION": "최신 버전",
  "Connected to Server API": "서버 API에 연결됨",
  "Heartbeat within threshold": "임계값 내 하트비트",
  "Structured node reports": "구조화된 노드 보고서",
  "Initial development release": "초기 개발 릴리스",
  Version: "버전",
  Capabilities: "기능",
  "Last seen": "마지막 확인",
  "No agents registered": "등록된 에이전트 없음",
  "Server API is unavailable": "서버 API 사용 불가",
  "Host inventory": "호스트 인벤토리",
  "Latest hardware, operating system, runtime, VM, container, and process reports":
    "최신 하드웨어, 운영 체제, 런타임, VM, 컨테이너, 프로세스 보고서",
  Refresh: "새로고침",
  REPORTS: "보고서",
  AGENTS: "에이전트",
  CONTAINERS: "컨테이너",
  PROCESSES: "프로세스",
  "Latest report per Agent": "에이전트별 최신 보고서",
  "Registered fleet": "등록된 플릿",
  "Discovered guests": "발견된 게스트",
  "Discovered runtimes": "발견된 런타임",
  "Observed node processes": "관찰된 노드 프로세스",
  Host: "호스트",
  OS: "OS",
  Disks: "디스크",
  Interfaces: "인터페이스",
  Observed: "관찰됨",
  "View report": "보고서 보기",
  "Waiting for Agent inventory reports": "에이전트 인벤토리 보고서 대기 중",
  "Agent inventory": "에이전트 인벤토리",
  "Inventory report unavailable": "인벤토리 보고서 사용 불가",
  "Waiting for the first inventory report": "첫 인벤토리 보고서 대기 중",
  "Network interfaces": "네트워크 인터페이스",
  "unknown model": "알 수 없는 모델",
  "no address": "주소 없음",
  "Back to fleet": "플릿으로 돌아가기",
  "Refresh inventory": "인벤토리 새로고침",
  "CPU CORES": "CPU 코어",
  DISKS: "디스크",
  "Block devices": "블록 장치",
  "Not detected or unavailable": "감지되지 않았거나 사용 불가",

  // RBAC
  "Manage permissions by operational responsibility":
    "운영 책임별로 권한을 관리합니다",
  "Test access": "접근 테스트",
  "+ Create role": "+ 역할 생성",
  Permissions: "권한",
  Usage: "사용",
  Custom: "사용자 정의",
  View: "보기",
  "Access scopes": "접근 범위",
  "Manage hierarchical resource access boundaries":
    "계층적 리소스 접근 경계를 관리합니다",
  "+ Create scope": "+ 범위 생성",
  Scope: "범위",
  "Hierarchy paths": "계층 경로",
  "Tag selector": "태그 셀렉터",
  "Assign roles to users and teams within hierarchy scopes":
    "계층 범위 내에서 사용자와 팀에 역할을 할당합니다",
  "+ Create binding": "+ 바인딩 생성",
  Subject: "주체",
  Expires: "만료",
  Never: "없음",
  "No role bindings": "역할 바인딩 없음",

  // Jobs / operations
  "Track approved operations executed through managed agents":
    "관리 에이전트를 통해 실행된 승인 작업을 추적합니다",
  "+ Run diagnostic": "+ 진단 실행",
  TOTAL: "전체",
  RUNNING: "실행 중",
  APPROVAL: "승인",
  SUCCEEDED: "성공",
  "Server operations": "서버 작업",
  "Agent processing": "에이전트 처리 중",
  "Independent review": "독립 검토",
  "Completed operations": "완료된 작업",
  Operation: "작업",
  "Requested by": "요청자",
  Reason: "사유",
  Approve: "승인",
  "No operations requested": "요청된 작업 없음",

  // Incidents
  "Coordinate response and document infrastructure incidents":
    "인프라 장애 대응을 조율하고 문서화합니다",
  "+ Declare incident": "+ 장애 선언",
  ACTIVE: "활성",
  INVESTIGATING: "조사 중",
  MITIGATING: "완화 중",
  "Declared incidents": "선언된 장애",
  "Actions in progress": "진행 중인 조치",
  "Closed incidents": "종료된 장애",
  Severity: "심각도",
  Message: "메시지",
  "CPU of capacity": "CPU 가용량 대비",
  "Memory of capacity": "메모리 가용량 대비",
  Commander: "지휘자",
  Unassigned: "미할당",
  "No active incidents": "활성 장애 없음",
  "Infrastructure incident": "인프라 장애",
  "+ Note": "+ 노트",
  "Change status": "상태 변경",
  "Incident timeline": "장애 타임라인",
  "Loading timeline": "타임라인 로딩 중",
  "Incident details": "장애 세부 정보",

  // Alert rules / silences / routing
  "Manage metric thresholds, duration, severity, and hierarchy scope":
    "메트릭 임계값, 지속 시간, 심각도, 계층 범위를 관리합니다",
  "+ Silence window": "+ 무음 창",
  "+ New rule": "+ 새 규칙",
  Rule: "규칙",
  Condition: "조건",
  Duration: "지속 시간",
  Enabled: "활성화",
  Disabled: "비활성화",
  "No alert rules configured": "구성된 알림 규칙 없음",
  "Silence windows": "무음 창",
  Selector: "셀렉터",
  Window: "기간",
  "No silence windows": "무음 창 없음",
  "Inhibition policies, webhook routes, and delivery status":
    "중복 알림 차단, 웹훅 라우트, 전달 상태",
  "+ Inhibition": "+ 차단 규칙",
  "+ Channel": "+ 채널",
  "+ Route": "+ 라우트",
  "Inhibition policies": "중복 알림 차단",
  "Equal labels": "동일 레이블",
  "same resource": "동일 리소스",
  "No inhibition policies": "차단 규칙 없음",
  "Webhook channels": "웹훅 채널",
  "No channels": "채널 없음",
  Routes: "라우트",
  "all severities": "모든 심각도",
  "No routes": "라우트 없음",
  "Recent deliveries": "최근 전달",
  Time: "시간",
  Alert: "알림",
  Event: "이벤트",
  Channel: "채널",
  Attempts: "시도 횟수",
  "No deliveries": "전달 없음",

  // Runbooks / executions
  "Predefined, reviewable infrastructure diagnosis and recovery":
    "사전 정의되고 검토 가능한 인프라 진단 및 복구",
  "+ Create runbook": "+ 런북 생성",
  Risk: "위험도",
  Steps: "단계",
  Description: "설명",
  Run: "실행",
  "No runbooks configured": "구성된 런북 없음",
  "Recent executions": "최근 실행",
  Execution: "실행",
  "No executions": "실행 없음",

  // Terminal
  "PTY stream": "PTY 스트림",
  Connected: "연결됨",
  Connecting: "연결 중",
  "Type a command and press Enter": "명령을 입력하고 Enter를 누르세요",
  "This identity has read-only terminal access.":
    "이 자격 증명은 읽기 전용 터미널 접근 권한이 있습니다.",
  "Close session": "세션 닫기",
  "No active terminal session. Create a request and obtain independent approval.":
    "활성 터미널 세션이 없습니다. 요청을 생성하고 독립 승인을 받으세요.",
  "Remote terminal": "원격 터미널",
  "Approval-controlled and audited sessions to managed nodes":
    "관리 노드에 대한 승인 제어 및 감사 세션",
  "+ Request session": "+ 세션 요청",
  "Session requests": "세션 요청",
  Recording: "녹화",
  "No sessions": "세션 없음",
  "● Active · Connected": "● 활성 · 연결됨",
  "● Active · Connecting": "● 활성 · 연결 중",
  "● Active · Reconnecting": "● 활성 · 다시 연결 중",

  // Audit
  "Immutable activity trail for API, Agent, approval, and terminal changes":
    "API, 에이전트, 승인, 터미널 변경에 대한 불변 활동 기록",
  "Export page": "페이지 내보내기",
  Actor: "행위자",
  Result: "결과",

  // Generic / placeholder pages
  "Automate repeatable infrastructure diagnosis and recovery":
    "반복 가능한 인프라 진단 및 복구를 자동화합니다",
  "Asset inventory": "자산 인벤토리",
  "Search hardware, software, ownership, and lifecycle data":
    "하드웨어, 소프트웨어, 소유권, 수명 주기 데이터를 검색합니다",
  "Review infrastructure changes and operator activity":
    "인프라 변경 및 운영자 활동을 검토합니다",
  "Schedule and manage maintenance windows": "유지보수 창을 예약하고 관리합니다",
  "Track bulk, scheduled, and remote operations":
    "대량, 예약, 원격 작업을 추적합니다",
  "Manage rule-based resource membership": "규칙 기반 리소스 멤버십을 관리합니다",
  "Manage resource classification keys and values":
    "리소스 분류 키와 값을 관리합니다",
  "Manage users and access status": "사용자와 접근 상태를 관리합니다",
  "Manage ownership and team membership": "소유권과 팀 멤버십을 관리합니다",
  "Access requests": "접근 요청",
  "Review temporary and elevated access requests":
    "임시 및 상승된 접근 요청을 검토합니다",
  Management: "관리",
  "Manage KloudView resources": "KloudView 리소스를 관리합니다",
  "+ Create": "+ 생성",
  HEALTHY: "정상",
  ATTENTION: "주의",
  UPDATED: "업데이트됨",
  "All managed resources": "전체 관리 리소스",
  "Requires review": "검토 필요",
  "Live data": "실시간 데이터",
  "Recent activity": "최근 활동",

  // Modals — form labels
  NAME: "이름",
  TYPE: "유형",
  TAGS: "태그",
  ATTRIBUTES: "속성",
  "e.g. legacy-node-01": "예: legacy-node-01",
  "Agent ownership and identity cannot be changed manually.":
    "에이전트 소유권과 자격 증명은 수동으로 변경할 수 없습니다.",
  "SUBJECT ID": "주체 ID",
  ROLE: "역할",
  SCOPE: "범위",
  "EXPIRES AT": "만료 시각",
  "user or team ID": "사용자 또는 팀 ID",
  METRIC: "메트릭",
  CONDITION: "조건",
  DURATION: "지속 시간",
  SEVERITY: "심각도",
  ENABLED: "활성화",
  "TAG SELECTOR": "태그 선택자",
  START: "시작",
  END: "종료",
  "Planned rack maintenance": "예정된 랙 유지보수",
  "Matching active alerts become silenced. New matching alerts are suppressed during this window.":
    "일치하는 활성 알림은 무음 처리됩니다. 이 기간 동안 새로 일치하는 알림도 전송되지 않습니다.",
  "SOURCE SEVERITY": "소스 심각도",
  "TARGET SEVERITY": "대상 심각도",
  "EQUAL LABELS": "동일 레이블",
  "Critical suppresses warning": "심각 발생 시 경고 차단",
  "cluster, service · empty means same resource":
    "cluster, service · 비어 있으면 동일 리소스",
  "WEBHOOK URL": "웹훅 URL",
  "Platform webhook": "플랫폼 웹훅",
  CHANNELS: "채널",
  SEVERITIES: "심각도",
  EVENTS: "이벤트",
  "Critical production": "프로덕션 심각",
  CONTINUE: "계속",
  "Stop after route": "경로 후 중지",
  "Continue matching": "계속 매칭",
  OPERATION: "작업",
  SERVICE: "서비스",
  "containerd.service · service operations only":
    "containerd.service · 서비스 작업 전용",
  RISK: "위험도",
  DESCRIPTION: "설명",
  "Node inventory refresh": "노드 인벤토리 새로고침",
  "+ Add step": "+ 단계 추가",
  "ROLE NAME": "역할 이름",
  PERMISSIONS: "권한",
  "System roles are immutable.": "시스템 역할은 변경할 수 없습니다.",
  "+ Add permission": "+ 권한 추가",
  "GROUP NAME": "그룹 이름",
  "e.g. Rack-08": "예: Rack-08",
  "GROUP TYPE": "그룹 유형",
  "Physical rack": "물리 랙",
  Cluster: "클러스터",
  Environment: "환경",
  PARENT: "상위",
  "No parent": "상위 없음",
  PATH: "경로",
  "MEMBERSHIP MODE": "멤버십 모드",
  Static: "정적",
  Dynamic: "동적",
  "SCOPE NAME": "범위 이름",
  "HIERARCHY PATH": "계층 경로",
  "System scopes are immutable.": "시스템 범위는 변경할 수 없습니다.",
  SUBJECT: "주체",
  "RESOURCE / ACTION": "리소스 / 동작",
  "● Allowed": "● 허용됨",
  TARGET: "대상",
  REASON: "사유",
  "Incident, ticket, or operational reason": "장애, 티켓 또는 운영 사유",
  "ACCESS REASON": "접근 사유",
  "TARGET NODE": "대상 노드",
  "No connected agents": "연결된 에이전트 없음",
  "OPERATION NOTE": "작업 메모",
  "Reason for this action": "이 작업의 사유",
  NOTE: "메모",
  "Optional note": "선택 메모",
  TITLE: "제목",
  RESOURCE: "리소스",
  "RELATED ALERT": "관련 알림",
  "No related alert": "관련 알림 없음",
  COMMANDER: "지휘자",
  "Customer-facing impact": "고객 대상 영향",
  ASSIGNEE: "담당자",
  SUMMARY: "요약",
  STATUS: "상태",
  "ASSIGN TO": "할당 대상",
  "Me (Boan Kim)": "나 (Boan Kim)",
  "Platform Operations": "플랫폼 운영팀",
  "Network Team": "네트워크 팀",
  "Add investigation notes…": "조사 메모 추가…",
  "Investigation finding or mitigation update": "조사 결과 또는 완화 업데이트",
  "Operational reason and related incident": "운영 사유 및 관련 장애",
  RESULT: "결과",
  "REQUESTED BY": "요청자",
  "DEFAULT GROUPING": "기본 그룹화",
  "HEATMAP METRIC": "히트맵 메트릭",
  "RESOURCE PATH": "리소스 경로",
  SOURCE: "소스",
  RELATION: "관계",
  hosts: "호스팅",
  runs: "실행",
  contains: "포함",
  "depends on": "종속",
  "role=database": "role=database",

  // Modals — titles and messages
  "Create runbook": "런북 생성",
  "Edit runbook": "런북 편집",
  "Delete runbook": "런북 삭제",
  "The runbook definition will be removed. Execution history remains.":
    "런북 정의가 제거됩니다. 실행 이력은 유지됩니다.",
  "Execute runbook": "런북 실행",
  "Steps execute sequentially. High-risk runbooks require independent approval.":
    "단계는 순차적으로 실행됩니다. 고위험 런북은 독립적인 승인이 필요합니다.",
  "Start execution": "실행 시작",
  "Approve runbook execution": "런북 실행 승인",
  "The execution plan and targets were reviewed. Approval will release the first blocked step.":
    "실행 계획과 대상이 검토되었습니다. 승인하면 처음 차단된 단계가 해제됩니다.",
  "Request terminal session": "터미널 세션 요청",
  "Independent approval required. Session input and output will be audited.":
    "독립적인 승인이 필요합니다. 세션 입력과 출력이 감사됩니다.",
  "Request session": "세션 요청",
  "Approve terminal session": "터미널 세션 승인",
  "Approval will activate this audited terminal session. The requester and approver are recorded separately.":
    "승인하면 감사 대상 터미널 세션이 활성화됩니다. 요청자와 승인자는 별도로 기록됩니다.",
  "Close terminal session": "터미널 세션 닫기",
  "The active session will be closed and retained in the audit trail.":
    "활성 세션이 닫히고 감사 추적에 보관됩니다.",
  "Masked terminal recording": "마스킹된 터미널 녹화",
  truncated: "잘림",
  "Download JSON": "JSON 다운로드",
  Close: "닫기",
  "Delete terminal recording": "터미널 녹화 삭제",
  "This permanently deletes the masked input and output recording.":
    "마스킹된 입력 및 출력 녹화가 영구적으로 삭제됩니다.",
  "Delete recording": "녹화 삭제",
  "Create node group": "노드 그룹 생성",
  "Edit node group": "노드 그룹 편집",
  "Manage members": "멤버 관리",
  "Dynamic membership:": "동적 멤버십:",
  "CURRENT MEMBERS": "현재 멤버",
  "No members": "멤버 없음",
  "ADD RESOURCES": "리소스 추가",
  "All resources assigned": "모든 리소스가 할당됨",
  "Delete node group": "노드 그룹 삭제",
  "The group will be removed. Member resources remain available.":
    "그룹이 제거됩니다. 멤버 리소스는 계속 사용할 수 있습니다.",
  "Request secure terminal session": "보안 터미널 세션 요청",
  "Independent approval is required before commands can run.":
    "명령을 실행하기 전에 독립적인 승인이 필요합니다.",
  Request: "요청",
  "This will restart the selected service on node-042.":
    "node-042에서 선택한 서비스를 재시작합니다.",
  "A brief service interruption may occur.": "짧은 서비스 중단이 발생할 수 있습니다.",
  "Restart service": "서비스 재시작",
  "Enter maintenance mode": "유지보수 모드 시작",
  "1 hour": "1시간",
  "4 hours": "4시간",
  "Until manually disabled": "수동으로 비활성화할 때까지",
  "Enable maintenance": "유지보수 활성화",
  "Disconnect terminal?": "터미널 연결을 끊으시겠습니까?",
  "The active terminal session will be closed and saved to the audit log.":
    "활성 터미널 세션이 닫히고 감사 로그에 저장됩니다.",
  Disconnect: "연결 끊기",
  "Export complete": "내보내기 완료",
  "Approve high-risk operation": "고위험 작업 승인",
  "The requester cannot approve their own operation.":
    "요청자는 자신의 작업을 승인할 수 없습니다.",
  "Approve operation": "작업 승인",
  Notifications: "알림",
  "This workflow is connected to the interactive prototype.":
    "이 워크플로우는 대화형 프로토타입에 연결되어 있습니다.",
  "Permission denied": "권한 거부",
  "Resource lookup failed": "리소스 조회 실패",
  "Critical infrastructure condition is currently firing.":
    "심각한 인프라 상태가 현재 발생 중입니다.",
  "Metric refresh failed": "지표 새로고침 실패",
  "Terminal unavailable": "터미널 사용 불가",
  "The PTY stream is not connected.": "PTY 스트림이 연결되어 있지 않습니다.",
  "Terminal connection failed": "터미널 연결 실패",
  "Terminal refresh failed": "터미널 새로고침 실패",
  "Resource search failed": "리소스 검색 실패",
  "Overview refresh failed": "개요 새로고침 실패",
  "Audit refresh failed": "감사 새로고침 실패",
  "Membership removed": "멤버십 제거됨",
  "Resource removed from static group.": "정적 그룹에서 리소스가 제거되었습니다.",
  "Request failed": "요청 실패",
  "Request completed successfully.": "요청이 성공적으로 완료되었습니다.",
  "Customize overview": "개요 사용자 지정",
  "Environment scope": "환경 범위",
  "The selected path is validated against the current identity before data is loaded.":
    "선택한 경로는 데이터를 로드하기 전에 현재 자격 증명에 대해 검증됩니다.",
  "KloudView help": "KloudView 도움말",
  "Fleet health, utilization, and anomaly triage":
    "플릿 상태, 사용률 및 이상 징후 분류",
  "Resource hierarchy and CRUD": "리소스 계층 및 CRUD",
  "Enrollment and inventory state": "등록 및 인벤토리 상태",
  "Approval-controlled terminal and runbooks": "승인 제어 터미널 및 런북",
  "Project documentation: README.md and docs/":
    "프로젝트 문서: README.md 및 docs/",
  "API endpoint": "API 엔드포인트",
  Identity: "아이덴티티",
  "API state": "API 상태",
  "Install Agent": "에이전트 설치",
  "Build the single binary and install the supplied systemd unit.":
    "단일 바이너리를 빌드하고 제공된 systemd 유닛을 설치하세요.",
  "Set the Server URL and enrollment token in /etc/kloudview/agent.env before starting the service.":
    "서비스를 시작하기 전에 /etc/kloudview/agent.env에 서버 URL과 등록 토큰을 설정하세요.",
  "Agent upgrade": "에이전트 업그레이드",
  "Remote upgrade remains locked until a signed release source is configured. This prevents an unsigned binary rollout from the management plane.":
    "서명된 릴리스 소스가 구성될 때까지 원격 업그레이드는 잠긴 상태로 유지됩니다. 이는 관리 플레인에서 서명되지 않은 바이너리 배포를 방지합니다.",
  "Use the packaged systemd deployment workflow for the current development build.":
    "현재 개발 빌드에는 패키지된 systemd 배포 워크플로를 사용하세요.",
  "Hierarchy selected": "계층 선택됨",
  "Use View resources to inspect this scope.":
    "이 범위를 검사하려면 리소스 보기를 사용하세요.",
  "Unavailable route": "사용할 수 없는 경로",
  "This page is not part of the active navigation.":
    "이 페이지는 활성 내비게이션에 포함되어 있지 않습니다.",
  "Add unmanaged resource": "비관리 리소스 추가",
  "Create resource": "리소스 생성",
  "Add resource relation": "리소스 관계 추가",
  "Create relation": "관계 생성",
  "Delete resource relation": "리소스 관계 삭제",
  "The resources remain available. Only this topology edge is removed.":
    "리소스는 계속 사용할 수 있습니다. 이 토폴로지 엣지만 제거됩니다.",
  "Delete unmanaged resource": "비관리 리소스 삭제",
  "Relations, group memberships, and stored metrics for this resource will also be removed.":
    "이 리소스의 관계, 그룹 멤버십 및 저장된 메트릭도 함께 제거됩니다.",
  "Create role": "역할 생성",
  "System role": "시스템 역할",
  "Edit role": "역할 편집",
  "Delete custom role": "사용자 정의 역할 삭제",
  "This role will be removed after active bindings are reviewed.":
    "이 역할은 활성 바인딩을 검토한 후 제거됩니다.",
  "Create access scope": "접근 범위 생성",
  "Create scope": "범위 생성",
  "System scope": "시스템 범위",
  "Edit access scope": "접근 범위 편집",
  "Delete access scope": "접근 범위 삭제",
  "Scopes referenced by role bindings cannot be deleted.":
    "역할 바인딩에서 참조하는 범위는 삭제할 수 없습니다.",
  "Create role binding": "역할 바인딩 생성",
  "Create binding": "바인딩 생성",
  "Edit role binding": "역할 바인딩 편집",
  "Delete role binding": "역할 바인딩 삭제",
  "Assigned access will be revoked immediately.":
    "할당된 접근 권한이 즉시 취소됩니다.",
  "Access simulator": "접근 시뮬레이터",
  "Remove offline agent": "오프라인 에이전트 제거",
  "The Agent record, discovered resources, inventory, relations, and metrics will be removed.":
    "에이전트 레코드, 검색된 리소스, 인벤토리, 관계 및 메트릭이 제거됩니다.",
  "Update alert": "알림 업데이트",
  "Delete alert": "알림 삭제",
  "The alert record will be removed.": "알림 레코드가 제거됩니다.",
  "Add incident note": "장애 노트 추가",
  "Add note": "노트 추가",
  "Change incident status": "장애 상태 변경",
  "Update status": "상태 업데이트",
  "Delete incident": "장애 삭제",
  "The incident and its timeline will be removed.":
    "장애와 해당 타임라인이 제거됩니다.",
  "Run agent diagnostic": "에이전트 진단 실행",
  "Check service status": "서비스 상태 확인",
  "Service restart requires independent approval. Agent allowlist enforcement remains authoritative.":
    "서비스 재시작에는 독립적인 승인이 필요합니다. 에이전트 허용 목록 적용이 최종 권한을 갖습니다.",
  "Run diagnostic": "진단 실행",
  "Declare incident": "장애 선언",
  "Create silence window": "무음 창 생성",
  "Create silence": "무음 생성",
  "Edit silence window": "무음 창 편집",
  "Delete silence window": "무음 창 삭제",
  "Matching alert rules will resume evaluation immediately.":
    "일치하는 알림 규칙이 즉시 평가를 재개합니다.",
  "Create alert rule": "알림 규칙 생성",
  "Create rule": "규칙 생성",
  "Edit alert rule": "알림 규칙 편집",
  "Delete alert rule": "알림 규칙 삭제",
  "The selected rule will stop evaluating new metric samples.":
    "선택한 규칙은 새 메트릭 샘플 평가를 중지합니다.",
  "Edit inhibition": "차단 규칙 편집",
  "Create inhibition": "차단 규칙 생성",
  "Delete inhibition": "차단 규칙 삭제",
  "Suppressed alerts may become eligible for notification immediately.":
    "차단되던 알림이 즉시 전송 대상이 됩니다.",
  "Edit webhook channel": "웹훅 채널 편집",
  "Create webhook channel": "웹훅 채널 생성",
  "Delete channel": "채널 삭제",
  "Channels referenced by a route cannot be deleted.":
    "경로에서 참조하는 채널은 삭제할 수 없습니다.",
  "Edit notification route": "알림 경로 편집",
  "Create notification route": "알림 경로 생성",
  "Delete route": "경로 삭제",
  "New alerts will no longer be sent through this route.":
    "새 알림은 더 이상 이 경로를 통해 전송되지 않습니다.",

  // Fragments split across inline <b>/<span> tags (each is its own text node)
  "healthy ·": "정상 ·",
  "warn ·": "경고 ·",
  crit: "심각",
  "API connected": "API 연결됨",
  "API offline": "API 오프라인",
};

// Patterns for text nodes that embed dynamic values (numbers, timestamps) and
// therefore cannot be matched by an exact dictionary key. Applied only after an
// exact lookup misses. Each entry maps a RegExp to a builder over its matches.
// A connectivity alert summary is assembled on the server from fixed segments
// joined by "; ", in combinations that depend on what was observed. Translating
// each segment on its own covers every combination without one regex per shape.
// Go renders a duration as "1m58s"; Korean reads it as time, not as tokens.
const koDuration = (value) =>
  value
    .replace(/(\d+)h/, "$1시간 ")
    .replace(/(\d+)m(?!s)/, "$1분 ")
    .replace(/([\d.]+)s/, "$1초")
    .trim();

const ALERT_SUMMARY_SEGMENTS = [
  [
    /^last seen (.+), final cpu ([\d.]+)% memory ([\d.]+)% disk ([\d.]+)%$/,
    (m) => `마지막 수신 ${m[1]}, 최종 CPU ${m[2]}% 메모리 ${m[3]}% 디스크 ${m[4]}%`,
  ],
  [/^last seen (.+)$/, (m) => `마지막 수신 ${m[1]}`],
  [
    /^no other node went silent — suspect this host$/,
    () => "동시에 중단된 노드 없음 — 이 호스트 자체 문제로 추정",
  ],
  [
    /^(\d+) other nodes went silent within (.+) — suspect shared power or network$/,
    (m) =>
      `${koDuration(m[2])} 이내 다른 노드 ${m[1]}대도 중단 — 전원·네트워크 공통 장애로 추정`,
  ],
  [/^reporting resumed after (.+)$/, (m) => `${koDuration(m[1])} 후 보고 재개`],
];

function translateAlertSummary(text) {
  const parts = text.split("; ");
  const translated = parts.map((part) => {
    for (const [re, build] of ALERT_SUMMARY_SEGMENTS) {
      const match = re.exec(part);
      if (match) return build(match);
    }
    return part;
  });
  // Undefined leaves the original in place, rather than reporting a translation
  // that changed nothing.
  return translated.some((part, i) => part !== parts[i])
    ? translated.join("; ")
    : undefined;
}

const patterns = [
  [/^Share of (\d+) vCPU$/, (m) => `vCPU ${m[1]}개 대비`],
  [/^Show (\d+) before it was declared$/, (m) => `선언 전 ${m[1]}건 보기`],
  [/^Show (\d+) checks$/, (m) => `확인 작업 ${m[1]}건 보기`],
  [/^Hide (\d+) checks$/, (m) => `확인 작업 ${m[1]}건 숨기기`],
  [/^Hide (\d+) before it was declared$/, (m) => `선언 전 ${m[1]}건 숨기기`],
  [/^Agent stopped reporting: (.+)$/, (m) => `에이전트 보고 중단: ${m[1]}`],
  [/^last seen .+/, (m) => translateAlertSummary(m.input)],
  [/^Maximum (.+)$/, (m) => `최대 ${m[1]}`],
  [/^All nodes (\d+)$/, (m) => `전체 노드 ${m[1]}`],
  [/^All (\d+)$/, (m) => `전체 ${m[1]}`],
  [/^Node (\d+)$/, (m) => `노드 ${m[1]}`],
  [/^Manage members · (.+)$/, (m) => `멤버 관리 · ${m[1]}`],
  [
    /^(All units|System|Authentication|Kernel) · (.+)$/,
    (m) => `${ko[m[1]] ?? m[1]} · ${m[2]}`,
  ],
  [/^Hypervisor (\d+)$/, (m) => `하이퍼바이저 ${m[1]}`],
  [/^(\d+) shown · (\d+) selected$/, (m) => `${m[1]}개 표시 · ${m[2]}개 선택`],
  [/^Container (\d+)$/, (m) => `컨테이너 ${m[1]}`],
  [/^VM (\d+)$/, (m) => `VM ${m[1]}`],
  [/^Process (\d+)$/, (m) => `프로세스 ${m[1]}`],
  [/^(\d+) of (\d+)$/, (m) => `${m[2]}개 중 ${m[1]}개`],
  [/^(\d+) of (\d+) · (\d+) not reporting usage$/, (m) => `${m[2]}개 중 ${m[1]}개 · ${m[3]}개 사용량 미보고`],
  [/^([\d.]+)% in use$/, (m) => `${m[1]}% 사용 중`],
  [/^(\d+) active alerts$/, (m) => `활성 알림 ${m[1]}건`],
  [/^Execution · (.+)$/, (m) => `실행 · ${m[1]}`],
  [/^Last update (.+)$/, (m) => `마지막 갱신 ${m[1]}`],
  [/^(\d+) shown$/, (m) => `${m[1]}개 표시`],
  [/^(\d+) samples in bucket$/, (m) => `구간 표본 ${m[1]}개`],
  [/^(\d+) resources reporting$/, (m) => `보고 중인 리소스 ${m[1]}개`],
  [/^(\d+) warnings? across the fleet$/, (m) => `전체 인프라 경고 ${m[1]}건`],
  [/^Mean per resource · peak ([\d.]+)%$/, (m) => `리소스 평균 · 최대 ${m[1]}%`],
  [
    /^Every severity but debug streams in as it happens\. The (\d+) debug entries in this window are counted only — reach them with a journal read\.(?: Held since (.+)\.)?$/,
    (m) =>
      `디버그를 제외한 모든 심각도가 발생 즉시 들어옵니다. 이 구간의 디버그 ${m[1]}건은 개수만 집계되며, 저널 조회로 확인하세요.${m[2] ? ` ${m[2]}부터 보관 중입니다.` : ""}`,
  ],
  [/^(\d+)–(\d+) of (\d+)$/, (m) => `${m[3]}개 중 ${m[1]}–${m[2]}`],
  [/^(\d+) of (\d+) match$/, (m) => `${m[2]}개 중 ${m[1]}개 일치`],
  // An agent's granted capabilities arrive as one comma-separated text node.
  [
    /^(inventory|metrics|terminal|logs)(, (inventory|metrics|terminal|logs))*$/,
    (m) => m[0].split(", ").map((word) => ko[word] ?? word).join(", "),
  ],
  [/^(\d+) critical · (\d+) active incidents?$/, (m) => `심각 ${m[1]}건 · 진행 중 장애 ${m[2]}건`],
  [/^peak (.+)$/, (m) => `최대 ${m[1]}`],
  [/^([\d.]+) \/ (\d+) cores · (\d+) free$/, (m) => `${m[1]} / ${m[2]} 코어 · ${m[3]} 여유`],
  [/^(\d+) cores · (.+)$/, (m) => `${m[1]} 코어 · ${m[2]}`],
  [/^(\d+) cores$/, (m) => `${m[1]} 코어`],
  [/^(\d+) nodes · (\d+) cores · (.+)$/, (m) => `노드 ${m[1]} · 코어 ${m[2]} · ${m[3]}`],
  [/^(\d+) nodes$/, (m) => `노드 ${m[1]}개`],
  [/^(\d+) bindings$/, (m) => `바인딩 ${m[1]}개`],
  [/^(\d+)–(\d+) of (\d+) resources$/, (m) => `리소스 ${m[3]}개 중 ${m[1]}–${m[2]}`],
  [/^(\d+)–(\d+) of (\d+)$/, (m) => `${m[3]}개 중 ${m[1]}–${m[2]}`],
  [/^confidence (low|medium|high)$/, (m) => `신뢰도 ${{ low: "낮음", medium: "보통", high: "높음" }[m[1]]}`],
  [
    /^Reaches (\d+)% in (?:~)?(.+)$/,
    (m) => `${m[2]} 후 ${m[1]}% 도달`,
  ],
  [/^~(\d+) days$/, (m) => `약 ${m[1]}일`],
  [/^~(\d+) months$/, (m) => `약 ${m[1]}개월`],
  [/^([▲▼→]) ([+-]?[\d.]+%\/day)$/, (m) => `${m[1]} ${m[2].replace("/day", "/일")}`],
  [/^last (\d+)h · headroom line (\d+)% \(CPU \/ Memory \/ Disk\)$/, (m) => `최근 ${m[1]}시간 · 여유선 ${m[2]}% (CPU / 메모리 / 디스크)`],
  [
    /^Expires at (.+?)(?: · host (.+?))?(?: · from (.+?))?$/,
    (m) =>
      `만료 ${m[1]}${m[2] ? ` · 호스트 ${m[2]}` : ""}${m[3] ? ` · 대역 ${m[3]}` : ""}`,
  ],
  [
    /^Status changed to (declared|investigating|mitigating|monitoring|resolved)$/,
    (m) =>
      `상태가 ${{ declared: "선언됨", investigating: "조사 중", mitigating: "완화 중", monitoring: "관찰 중", resolved: "해결됨" }[m[1]]}(으)로 변경됨`,
  ],
  [/^(\d+) samples since (.+)$/, (m) => `${m[2]}부터 ${m[1]}개 표본`],
  [/^([\d.]+) \/ (\d+) cores$/, (m) => `${m[1]} / ${m[2]} 코어`],
  [/^(.+) · Observed (.+)$/, (m) => `${m[1]} · 수집 시각 ${m[2]}`],
  [
    /^Session (\S+) · Approved by (.+)$/,
    (m) => `세션 ${m[1]} · 승인자 ${m[2]}`,
  ],
  [/^(\d+) of (\d+)$/, (m) => `${m[2]}건 중 ${m[1]}건`],
  [/^Saturated (\d+)$/, (m) => `포화 ${m[1]}`],
  [/^Busy (\d+)$/, (m) => `바쁨 ${m[1]}`],
  [/^Idle (\d+)$/, (m) => `유휴 ${m[1]}`],
  [/^(\d+) of (\d+) nodes$/, (m) => `노드 ${m[2]}개 중 ${m[1]}개`],
  [
    /^(\d+) nodes · (\d+) VMs · (\d+) containers$/,
    (m) => `노드 ${m[1]} · VM ${m[2]} · 컨테이너 ${m[3]}`,
  ],
  [
    /^(\d+) nodes · CPU (.+)% · MEM (.+)% · DSK (.+)% · NET↓ (.+) · NET↑ (.+)$/,
    (m) =>
      `노드 ${m[1]} · CPU ${m[2]}% · 메모리 ${m[3]}% · 디스크 ${m[4]}% · 네트워크↓ ${m[5]} · 네트워크↑ ${m[6]}`,
  ],
  [/^(\d+) visible groups$/, (m) => `그룹 ${m[1]}개`],
  [/^(\d+) unknown$/, (m) => `${m[1]} 알 수 없음`],
  [/^([\d.]+%) coverage$/, (m) => `${m[1]} 커버리지`],
  [/^Showing (\d+) of (\d+) relations$/, (m) => `관계 ${m[2]}개 중 ${m[1]}개 표시`],
  [/^Showing (\d+) of (\d+)$/, (m) => `${m[2]}개 중 ${m[1]}개 표시`],
  [
    /^· (\d+) reporting resources · (.+)$/,
    (m) => `· 리소스 ${m[1]}개 보고 · ${m[2]}`,
  ],
];

// Resolve the Korean form of a trimmed English text node: exact dictionary
// first, then dynamic patterns. Returns undefined when nothing applies.
function translateText(trimmed) {
  const exact = ko[trimmed];
  if (exact !== undefined) return exact;
  for (const [re, build] of patterns) {
    const match = re.exec(trimmed);
    if (match) return build(match);
  }
  return undefined;
}

// Translate a single trimmed English string. Returns the input unchanged when
// the current language is English or there is no Korean entry.
export function t(text) {
  if (lang !== "ko") return text;
  return ko[text] ?? text;
}

// Text written into the DOM after render never passes through
// translateFragment, so it asks for the same dictionary-then-pattern lookup
// the walker would have given it.
export function translateLive(text) {
  if (lang !== "ko") return text;
  return translateText(text) ?? text;
}

const TRANSLATABLE_ATTRS = ["placeholder", "title", "aria-label"];

// Walk a DOM fragment and translate visible text nodes and user-facing
// attributes in place. Leading/trailing whitespace of each text node is
// preserved so inline spacing and layout are unaffected. No-op in English.
export function translateFragment(root) {
  if (lang !== "ko") return;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const textNodes = [];
  while (walker.nextNode()) textNodes.push(walker.currentNode);
  for (const node of textNodes) {
    if (node.parentElement && node.parentElement.closest("[data-i18n-skip]"))
      continue;
    const raw = node.nodeValue;
    const trimmed = raw.trim();
    if (!trimmed) continue;
    const translated = translateText(trimmed);
    if (translated === undefined) continue;
    const start = raw.indexOf(trimmed);
    node.nodeValue =
      raw.slice(0, start) + translated + raw.slice(start + trimmed.length);
  }
  for (const el of root.querySelectorAll("*")) {
    for (const attr of TRANSLATABLE_ATTRS) {
      const value = el.getAttribute(attr);
      if (value == null) continue;
      const translated = ko[value.trim()];
      if (translated !== undefined) el.setAttribute(attr, translated);
    }
  }
}
