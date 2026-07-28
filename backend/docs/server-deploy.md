# Server Deploy Guide

Target server:

- Host: `postgresql.rozzi.my.id`
- IP: `68.183.224.98`
- PostgreSQL: already running on the same server

## 1. Prepare Server

```bash
ssh root@68.183.224.98

apt update
apt install -y git curl build-essential
```

Install Go if it is not installed:

```bash
curl -LO https://go.dev/dl/go1.25.5.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.25.5.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >/etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version
```

## 2. Upload Or Clone Project

Recommended path:

```bash
mkdir -p /opt/vero-travel-agents
cd /opt/vero-travel-agents
```

Clone your repo here, or upload the project folder with `rsync/scp`.

Example with rsync from local machine:

```bash
rsync -av --exclude node_modules --exclude .git \
  /media/rozzi/Data/myCode/VeroAiTravelAgents/ \
  root@68.183.224.98:/opt/vero-travel-agents/
```

## 3. Configure Backend Env

On the server:

```bash
cd /opt/vero-travel-agents/backend
cp .env.example .env
nano .env
```

Use:

```env
APP_ENV=production
PORT=8080

DATABASE_HOST=127.0.0.1
DATABASE_PORT=5432
DATABASE_USER=vero_user
DATABASE_PASSWORD=change_this_to_a_long_random_db_password
DATABASE_NAME=vero_travel
DATABASE_SSLMODE=disable
DATABASE_URL=

JWT_SECRET=change_this_to_a_long_random_secret

# Set CIDR reverse proxy yang sah. Jika API di-deploy di belakang Nginx/Caddy
# di server yang sama, gunakan 127.0.0.1. Biarkan kosong hanya jika API langsung
# terpapar ke internet tanpa proxy (SEC-14).
TRUSTED_PROXIES=127.0.0.1

AI_API_KEY=your_provider_key
AI_BASE_URL=https://api.openai.com/v1
AI_MODEL=gpt-4o-mini
```

Because backend and PostgreSQL are on the same server, prefer
`127.0.0.1` internally for DB connections. Use `postgresql.rozzi.my.id` only when connecting from
outside the server.

## 4. Build Backend

```bash
cd /opt/vero-travel-agents/backend
go mod tidy
go build -o vero-travel-api ./cmd/server
```

Test manually:

```bash
./vero-travel-api
```

Open another terminal:

```bash
curl http://127.0.0.1:8080/health
```

## 5. Install systemd Service

```bash
cp /opt/vero-travel-agents/backend/scripts/vero-travel-api.service /etc/systemd/system/vero-travel-api.service
chown -R www-data:www-data /opt/vero-travel-agents/backend
systemctl daemon-reload
systemctl enable vero-travel-api
systemctl start vero-travel-api
systemctl status vero-travel-api
```

View logs:

```bash
journalctl -u vero-travel-api -f
```

## 6. Open Firewall

If you want public API access:

```bash
ufw allow 8080/tcp
ufw reload
```

For production, put Nginx/Caddy in front and expose HTTPS instead of opening
port `8080` directly.

### A. Contoh Konfigurasi Nginx (`/etc/nginx/sites-available/vero-travel`)
```nginx
server {
    listen 80;
    server_name api.rozzi.my.id;

    # Redirect all HTTP requests to HTTPS
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name api.rozzi.my.id;

    ssl_certificate /etc/letsencrypt/live/api.rozzi.my.id/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.rozzi.my.id/privkey.pem;

    # Security Headers
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options DENY always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        
        # Websocket and SSE support
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID $request_id;
        
        # Disable buffering for SSE (Server-Sent Events)
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 24h;
    }
}
```

### B. Contoh Konfigurasi Caddy (`/etc/caddy/Caddyfile`)
```caddy
api.rozzi.my.id {
    # Auto TLS via Let's Encrypt / ZeroSSL

    header {
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        Referrer-Policy strict-origin-when-cross-origin
    }

    reverse_proxy 127.0.0.1:8080 {
        header_up Host {host}
        header_up X-Real-IP {remote}
        header_up X-Forwarded-For {client_ip}
        header_up X-Forwarded-Proto {scheme}
    }
}
```

