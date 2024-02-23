FROM python:3.11.6-alpine3.18
COPY requirements.txt .
RUN ["pip", "install", "-r", "requirements.txt"]
COPY *.py .
CMD ["python", "main.py"]
