## Running LoxiLB OAM with Database

This section provides detailed instructions on setting up and running **LoxiLB OAM** with both **insecure** and **secure** MySQL connections.

### (1) Manual Installation with Insecure DB Connection
This method runs **MySQL without SSL encryption**.

#### (a) Install MySQL
Run MySQL as a Docker container using `docker-compose`:
```sh
docker compose -f docker-compose.yml up -d
```

#### (b) Initialize MySQL Database
Execute the database initialization scripts:
```sh
./scripts/create_db.sh
```

#### (c) Install & Run OAM-LOXILB
Build and run the application:
```sh
make build
make run DB_USER=myuser DB_PASSWORD=mypassword DB_HOST=myhost DB_PORT=3306 DB_NAME=mydbname
```

### (2) Manual Installation with Secure DB Connection
This method enables **SSL encryption** between LoxiLB OAM and MySQL.

#### (a) Install MySQL with SSL (as Docker)
Run MySQL with SSL support:
```sh
./ssl/init_mysql.sh
```

#### (b) Configure SSL Certificates
Ensure that your MySQL SSL certificates are available:
```sh
SSL_CA="ssl/certs/root-ca.pem"
SSL_CERT="ssl/certs/client-cert.pem"
SSL_KEY="ssl/certs/client-key.pem"
```

#### (c) Install & Run OAM-LOXILB with SSL
Start the application with SSL-enabled database connection:
```sh
make build
make run DB_USER=myuser DB_PASSWORD=mypassword DB_HOST=myhost DB_PORT=3306 DB_NAME=mydbname SSL_CA=ssl/certs/root-ca.pem SSL_CERT=ssl/certs/client-cert.pem SSL_KEY=ssl/certs/client-key.pem ssl-option=true
```

### Notes:
- Ensure that MySQL is running before starting OAM-LOXILB.
- The `ssl-option=true` flag must be set when connecting to an **SSL-enabled MySQL instance**.
- If running inside Docker, ensure the certificates are **mounted** properly.

For additional details on the database schema, see
**[Database Schema Reference](oam-db.md)**.

