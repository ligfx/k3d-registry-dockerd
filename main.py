from flask import Flask, request
import docker
import tarfile
import json
import hashlib

def to_file_like_obj(iterable):
    chunk = b''
    offset = 0
    it = iter(iterable)

    def up_to_iter(size):
        nonlocal chunk, offset

        while size:
            if offset == len(chunk):
                try:
                    chunk = next(it)
                except StopIteration:
                    break
                else:
                    offset = 0
            to_yield = min(size, len(chunk) - offset)
            offset = offset + to_yield
            size -= to_yield
            yield chunk[offset - to_yield:offset]

    class FileLikeObj:
        def __init__(self):
            self._p = 0
        def tell(self):
            return self._p
        def read(self, size=-1):
            result =  b''.join(up_to_iter(float('inf') if size is None or size < 0 else size))
            self._p += len(result)
            return result
        def seek(self, where):
            if where >= self._p:
                self.read(where - self._p)
                return
            raise NotImplementedError(self._p, where)

    return FileLikeObj()

app = Flask(__name__)
client = docker.from_env()

@app.route("/")
def hello_world():
    return "Hello, world!"

@app.route("/v2/")
def v2():
    # return ('', 204)
    return "Hello, world!"

def find_blob(domain, namespace, image, reference):
    if not domain:
        # don't support acting as own repo
        return
    
    if not reference.startswith('sha256:'):
        raise NotImplementedError("Bad reference %r" % reference)
    
    name=f'{domain}/{namespace}/{image}'
    if name.startswith('docker.io/'):
        name = name[len('docker.io/'):]
    for image_obj in client.images.list(name):
        # TODO: could probably be smarter and keep a cache of which blobs belong to which images?
        t = tarfile.TarFile(fileobj=to_file_like_obj(image_obj.save()))
        while True:
            ti = t.next()
            if ti is None:
                break
            if ti.name == f'blobs/sha256/{reference.split("sha256:")[1]}':
                fi = t.extractfile(ti)
                return fi.read()
            if ti.name == 'index.json':
                fi = t.extractfile(ti)
                bdata = fi.read()
                m = hashlib.sha256()
                m.update(bdata)
                app.logger.info("trying index.json, digest %r" % m.hexdigest())
                if m.hexdigest() == reference.split("sha256:")[1]:
                    return bdata

@app.route("/v2/<namespace>/<image>/blobs/<reference>")
def blobs(namespace, image, reference):
    domain = request.args.get('ns')

    if request.method != 'GET':
        raise NotImplementedError()

    if not domain:
        # don't support acting as own repo
        return ("Requires a 'ns' query param", 500)

    blob = find_blob(domain, namespace, image, reference)
    if blob is None:
        return ('No blob found', 404)
    return (blob, 200)

@app.route("/v2/<namespace>/<image>/manifests/<reference>")
def manifests(namespace, image, reference):
    domain = request.args.get('ns')
    if not domain:
        # don't support acting as own repo
        return ('', 404)

    # TODO: check accept header

    if reference.startswith('sha256:'):
        blob = find_blob(domain, namespace, image, reference)
        if blob is None:
            return ('No manifest found', 404)
        return (blob, 200)

    def find_image():
        name=f'{domain}/{namespace}/{image}'
        if name.startswith('docker.io/'):
            name = name[len('docker.io/'):]
        for i in client.images.list(name):
            for t in i.tags:
                if t.count(":") != 1:
                    raise Exception(t)
                if t.split(":")[1] == reference:
                    return i
        return client.images.pull(name, tag=reference)

    image_obj = find_image()
    if not image_obj:
        return ('', 404)

    app.logger.info(repr(request.headers))

    if request.method == 'HEAD':
        return ('', 200)
    if request.method != 'GET':
        raise NotImplementedError()

    t = tarfile.TarFile(fileobj=to_file_like_obj(image_obj.save()))
    while True:
        ti = t.next()
        if ti is None:
            break
        app.logger.info(ti.name)
        if ti.name == 'index.json':
            fi = t.extractfile(ti)
            bdata = fi.read()
            m = hashlib.sha256()
            m.update(bdata)
            digest = m.hexdigest()
            app.logger.info(digest)
            return (bdata, 200, {"content-type": "application/vnd.oci.image.index.v1+json"})

        # fi = t.extractfile(ti)
        # if fi:
        #     bdata = fi.read(1024)
        #     if b'application/vnd.oci.image.manifest.v1+json' in bdata:
        #         bdata = bdata + fi.read()
        #         data = json.loads(bdata.decode('utf-8'))
        #         if data.get('mediaType') == 'application/vnd.oci.image.manifest.v1+json':
        #             app.logger.info(data)
        #             m = hashlib.sha256()
        #             m.update(bdata)
        #             m.hexdigest()
        #             # TODO: should we be returning the header
        #             g_manifests_seen[f'{namespace}/{image}/sha256:{m.hexdigest()}'] = bdata
        #             return (bdata, 200, {"content-type": 'application/vnd.oci.image.manifest.v1+json', 'Docker-Content-Digest': m.hexdigest()})
    return ('No manifest found', 500)

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=True)