# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS build

WORKDIR /src
COPY package.json package-lock.json ./
COPY apps/web/package.json ./apps/web/package.json
RUN npm ci

COPY apps/web ./apps/web
RUN npm run build

FROM nginx:1.29-alpine

COPY deploy/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /src/apps/web/dist /usr/share/nginx/html

ENTRYPOINT []
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
