#!/bin/sh

denyra_credentials_read() {
  denyra_credentials_file=$DENYRA_SECRETS_DIR/$1
  [ -r "$denyra_credentials_file" ] && [ -s "$denyra_credentials_file" ] || denyra_die "credential file is unavailable: $denyra_credentials_file"
  tr -d '\r\n' < "$denyra_credentials_file"
}

denyra_credentials() {
  printf 'Denyra     http://localhost:8090  admin  %s\n' "$(denyra_credentials_read bootstrap_admin)"
  printf 'Navidrome  http://localhost:4533  admin  %s\n' "$(denyra_credentials_read navidrome_admin)"
  printf 'SFTPGo     http://localhost:8080  admin  %s\n' "$(denyra_credentials_read sftpgo_admin)"
  printf 'SFTP       localhost:2022         upload %s\n' "$(denyra_credentials_read sftpgo_upload)"
  printf 'slskd      internal               admin  %s\n' "$(denyra_credentials_read slskd_web_password)"
  printf 'Soulseek   internal               %s  %s\n' "$(denyra_credentials_read soulseek_username)" "$(denyra_credentials_read soulseek_password)"
}
