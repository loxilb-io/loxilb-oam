# Public Cloud / Floating IP 자동 설치

[English](CONVERGED-CLOUD.md) · [Converged 구조](../../docs/deployment-converged.md)

Linux 한 대에 PostgreSQL, Gateway, OAM, UI를 설치하는 절차입니다.
선택 배포하는 Keycloak/syslog-ng는 별도 프로젝트이며
[연동 매뉴얼](INTEGRATIONS.ko.md)을 따릅니다. 모델 백엔드는 포함하지 않습니다.

## 1. 서버 주소와 공인 접속 주소

예시는 서버 NIC가 `10.0.0.14`이고, 클라우드 Floating IP `203.0.113.10`이
해당 VM에 연결된 구성입니다. 실제 환경의 값으로 바꾸세요.

| 설정 | 예시 | 용도 |
|---|---|---|
| `EDGE_BIND_IP` | `10.0.0.14` | 실제 서버 NIC에 할당된 주소 |
| `EDGE_HTTPS_PORT` | `8443` | UI/OAM HTTPS 포트 |
| `GW_HOST` | `gw.example.internal` | Gateway 인증서와 등록 정보의 호스트명 |
| `GW_CONNECT_IP` | `10.0.0.14` | OAM이 `GW_HOST`에 연결할 실제 내부 주소 |
| `EDGE_PUBLIC_ADDRESSES` | `203.0.113.10` | 브라우저 공인 접속 주소, NIC 설정과 별개 |

Floating IP를 NIC에 추가하거나 `EDGE_BIND_IP`, `GW_CONNECT_IP`에 넣지 않습니다.
Gateway 등록 주소도 Floating IP로 바꾸지 않습니다. 기존 호스트명을 유지해야
Gateway 인증서 검증이 정상 동작합니다. 내부 요청은 컨테이너 브리지를 통해
서버 NIC 주소로 전달되며 공인 NAT를 경유하지 않습니다.

필수 환경: Linux, Docker Engine, Docker Compose 2.24+ (`!override` 지원),
Python 3, OpenSSL, `ip` (iproute2), `curl`, `nsenter` (util-linux).
root 또는 sudo 권한으로 실행합니다. 네트워크 namespace 검증에는 root 권한이
필요합니다. 운영 환경에서는 승인한 이미지 버전/digest를 사용하세요.

클라우드 콘솔에서 Floating IP 연결을 확인하고 관리자 네트워크에 TCP 8443
인바운드를 허용합니다. 호스트 내부 검사만으로 외부 경로를 검증할 수는 없습니다.
DB, Gateway 평문 관리 포트, 선택 배포한 IdP/감사 수집기는 비공개로 유지합니다.
초기화 스크립트는 클라우드 방화벽이나 NIC를 수정하지 않습니다.

## 2. 신규 설치

```bash
sudo -i
cd /opt/loxilb-stack/loxilb-oam/deploy/compose
bash scripts/init-converged.sh
```

관리 콘솔 바인딩에는 실제 NIC 주소를 선택합니다. 브라우저에서 사용할 호스트명/IP와
추가 내부 이름을 입력합니다. 별도의 `Public cloud / NAT / Floating IP access`
단계에서 공인 IP 또는 DNS 이름을 쉼표로 구분해 입력합니다. 없으면 `-`입니다.
스크립트는 외부 IP 조회 서비스나 클라우드 메타데이터로 인바운드 Floating IP를
추측하지 않습니다.

공인 접속 주소를 명령행으로 지정할 수도 있습니다.

```bash
bash scripts/init-converged.sh --public-address 203.0.113.10
```

공인 URL은 `SITE_ADDRESS`, `OAM_ALLOWED_ORIGINS`에 추가됩니다. 인증서에는
IP면 `IP:`, 호스트명이면 `DNS:` SAN을 넣고, IP 접속에 필요한 SNI fallback도
설정합니다. `GW_CONNECT_IP`에는 실제 호스트 NIC 주소를 선택합니다. 콘솔이
wildcard 주소를 사용하면 기본 경로의 NIC 또는 유일한 NIC를 선택하고, 모호하면
명시적 선택을 요구하며 중단합니다. Docker 브리지, veth, loopback, dataplane
가상 인터페이스는 선택 대상에서 제외합니다.

생성된 `.env`, seed 파일, named volume을 보관하세요. 최초 로그인 후 관리자
비밀번호를 변경합니다. 자체 서명 인증서는 접속 PC에서 신뢰 설정이 필요합니다.
올바른 SAN만으로 발급자 신뢰까지 설정되지는 않습니다.

## 3. 기존 설치 복구 / 공인 접속 추가

기존 `.env`를 유지하도록 선택합니다. 이 경우에도 공인 주소 입력 단계가 실행되고,
DB 비밀번호, 암호화 키, Gateway 서비스 인증 정보는 보존됩니다. 주소는 기존
설정에 추가됩니다. `-`를 입력해도 이전 접속 주소를 삭제하지 않습니다.

```bash
cd /opt/loxilb-stack/loxilb-oam/deploy/compose
bash scripts/init-converged.sh --public-address 203.0.113.10
```

기존 자체 서명 인증서가 설정된 주소를 포함하지 않으면 재발급을 제안합니다.
이전 seed를 백업하고 Caddy만 중지해 인증서 볼륨을 갱신한 뒤 관리 프로젝트를
변경된 설정으로 시작합니다. 수동 볼륨 복사나 Compose 파일 수정은 필요 없습니다.
`--yes`는 이 기본 선택을 수락하므로 자동 실행 전 접속 주소를 확인하세요.

아직 유효한 자체 서명 인증서를 명시적으로 재발급하려면 다음 명령을 사용합니다.

```bash
bash scripts/init-converged.sh --yes --public-address 203.0.113.10 --refresh-edge-cert
```

운영자/CA 발급 인증서는 자체 서명 인증서로 자동 교체하지 않습니다. 검증 실패 시
모든 이름/IP를 포함하는 인증서와 키를 제공하세요. 이 설치 스크립트의 기존 ACME
모드는 DNS 이름만 허용합니다. 사용자 지정 `EDGE_TLS_SEED_DIR`도 반영합니다.
`backups/`는 접근이 제한되고 Git에서 제외됩니다. `.env` 네트워크 변경 전 원본은
`backups/network/`에 보관하며 인증서 seed와 볼륨 갱신 백업은 별도로 보관합니다.

NIC가 여러 개인 서버에서 다른 실제 IPv4 주소를 선택하려면 다음과 같이 실행합니다.

```bash
bash scripts/init-converged.sh --gateway-connect-ip 10.0.0.14 --public-address 203.0.113.10
```

서버에 없는 주소는 거부합니다. `GW_HOST`는 유지합니다. 올바른 `local-gateway`
등록은 관리자 비밀번호를 초기화하지 않고 재사용합니다. 관리 컨테이너 재생성 중에는
콘솔이 잠시 중단될 수 있습니다. 전체 초기화는 state/data 프로젝트도 확인하므로
이미지·설정이 변경된 경우 해당 서비스도 재시작될 수 있습니다. 업그레이드는 별도
유지보수 시간에 수행하세요.

`--no-start`는 서비스 시작 없이 설정/볼륨을 준비하는 옵션입니다. 실행 중인 인증서
볼륨 갱신이나 런타임 검증을 완료하는 옵션이 아닙니다. 복구에 `down -v`나 광범위한
Docker prune을 사용하지 마세요.

## 4. 완료 판정과 실패 처리

스크립트는 다음 항목을 검사합니다.

1. 기존 DB/schema/listener 및 Gateway 검사.
2. 실제 OAM 네트워크 namespace와 적용된 `/etc/hosts` 매핑.
3. OAM에 마운트된 공개 CA와 `GW_HOST`로 TLS 검증.
4. 익명 LB 관리 요청 401, 서비스 토큰을 사용한 버전·포트 조회 200.
   토큰은 curl stdin으로 전달하고 프로세스 인자에 넣지 않습니다.
5. 모든 콘솔 주소의 `/netlox/`, `/api/oam/health`가 검증된 TLS로 응답하는지 확인.
   목적지는 로컬 NIC이며, 이 검사는 로컬 주소·인증서 검증입니다.

검사가 실패하면 종료 코드는 0이 아니며 `Installation verification FAILED`를
표시합니다. 성공 설치로 처리하지 않습니다. 새 검사는 인증서 검증 우회를 사용하지
않습니다. Ubuntu OpenSSL 3.0과 최신 버전의 `x509 -checkip/-checkhost` 종료 동작이
달라, 인증서 이름 검사는 `openssl verify`를 사용합니다.

읽기 전용 검증만 별도로 실행할 수도 있습니다.

```bash
sudo python3 scripts/converged-network.py probe
python3 scripts/converged-network.py edge-probe
docker exec loxilb-mgmt-oam-loxilb-1 cat /etc/hosts
```

VM/NAT 외부의 관리자 PC에 공개 인증서 `certs/edge/cert.pem`만 안전하게 복사한 뒤
공인 경로를 확인합니다.

```bash
curl --cacert ./cert.pem --connect-timeout 5 --max-time 10 \
  -o /dev/null -sS -w 'HTTP %{http_code}, TLS verify %{ssl_verify_result}\n' \
  https://203.0.113.10:8443/netlox/
```

예상 결과는 `HTTP 200, TLS verify 0`입니다. 이어서 같은 `/netlox/` 주소를
브라우저로 열고 PC 정책에 따라 인증서를 신뢰한 뒤 로그인합니다. 인스턴스 화면을
새로고침해 `local-gateway` 상태와 버전을 확인하세요. CLI/API 검사만으로 브라우저
검증까지 통과했다고 판단하지 않습니다. 자체 서명 인증서 재발급 후에는 PC의 신뢰
설정/예외를 갱신해야 할 수 있습니다. 개인 키 `key.pem`은 PC로 복사하지 않습니다.

| 실패 | 조치 |
|---|---|
| 호스트 검사는 성공하지만 외부 TCP 연결 시간 초과 | 클라우드 인바운드, ACL, Floating IP 연결 확인 |
| OAM이 없는 `172.17.0.1`로 Gateway 연결 | 수정된 초기화 스크립트 재실행 후 `GW_CONNECT_IP` 확인 |
| TLS 이름/SAN 불일치 | 공인 주소 확인 후 자체 서명 재발급 또는 올바른 CA 인증서 제공 |
| 익명 관리 요청이 허용됨 | Gateway 관리 인증 조사, 설치 성공으로 처리하지 않음 |
| 인증 요청이 401/403 | 토큰 값을 출력하지 않고 서비스 토큰 보관·마운트 및 인증 모드 확인 |

복구를 위해 네트워크 `.env` 백업과 이전 인증서 seed를 보관합니다. 롤백 시 Caddy를
중지하고 서로 일치하는 `.env`와 인증서 seed를 복원한 뒤
`python3 scripts/prepare-converged-volumes.py --refresh edge-tls`를 실행합니다.
동일한 관리 Compose 파일 3개로 OAM/Caddy를 재생성하세요. Gateway/DB 볼륨과
다른 비밀값은 보존합니다. 이전 잘못된 매핑까지 복원하면 Down 증상도 재발하므로,
새 변경으로 발생한 회귀가 있을 때 롤백을 사용하세요.
