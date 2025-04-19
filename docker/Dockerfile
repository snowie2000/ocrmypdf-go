FROM jbarlow83/ocrmypdf-ubuntu AS base

ENV LANGUAGES=eng
EXPOSE 8080

COPY ./ocrmypdf-go /opt/ocrmypdf-go
COPY ./*.sh /opt/
		
RUN chmod +x /opt/ocrmypdf-go && chmod +x /opt/*.sh

ENTRYPOINT ["/opt/start.sh"]

