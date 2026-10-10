# WEB_BASE is a digest-pinned, reviewed static file server. It is not a backend.
ARG WEB_BASE
FROM ${WEB_BASE}
COPY web/ /usr/share/nginx/html/
